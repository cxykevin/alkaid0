package request

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/product"
	"github.com/cxykevin/alkaid0/provider/mask"
	"github.com/cxykevin/alkaid0/provider/request/structs"
)

var logger *log.LogsObj
var httpClient = &http.Client{
	Timeout: Timeout,
	Transport: &http.Transport{
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  false,
	},
}

func init() {
	logger = log.New("request")
}

// SimpleOpenAIRequest 发送 OpenAI ChatCompletion 请求（强制stream=true）。
// masker 非 nil 时，出站前对消息做敏感数据脱敏，并在流式响应中还原为原文。
func SimpleOpenAIRequest(ctx context.Context, baseURL, apiKey, model string, body structs.ChatCompletionRequest, masker *mask.Engine, callback func(structs.ChatCompletionResponse) error) error {
	// 设置模型和流式参数
	if body.Model == "" {
		body.Model = model
	}
	body.Stream = true

	baseURL = strings.TrimRight(baseURL, "/")
	logger.Info("call openai chat: %s", baseURL+ChatCompletionsEndpoint)

	// 出站脱敏：在序列化前替换敏感数据
	if masker != nil {
		body.Messages = masker.MaskMessages(body.Messages)
	}

	// 序列化请求体
	payload, err := json.Marshal(body)
	if err != nil {
		logger.Error("call openai chat error when marshal: %v", err)
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+ChatCompletionsEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		logger.Error("call openai chat error when create request: %v", err)
		return fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", product.UserAgent)

	// 发送请求
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error("call openai chat error when call: %v", err)
		return fmt.Errorf("failed to send request when call: %w", err)
	}
	defer resp.Body.Close()

	// 当 context 被取消时关闭 response body，以中断阻塞的 SSE 读取。
	// Go 的 http.Client 在请求发送后的 body 读取阶段不会检查 context，
	// 因此需要在单独的 goroutine 中监听取消信号并主动关闭连接。
	responseDone := make(chan struct{})
	defer close(responseDone)
	go func() {
		select {
		case <-ctx.Done():
			resp.Body.Close()
		case <-responseDone:
		}
	}()

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK {
		// 限制错误响应体大小，防止恶意/异常服务端发送无限响应体导致 OOM
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var errResp structs.ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			logger.Error("call openai chat error when unmarshal: %v", err)
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		logger.Error("call openai chat error when check stat %v", resp.StatusCode)
		logger.Debug("error body: %s", errResp.Error.Message)
		return fmt.Errorf("API error: %d %s", resp.StatusCode, errResp.Error.Message)
	}

	// 读取流式响应；部分兼容网关会忽略 stream=true 并返回普通 JSON，需兼容该响应形式。
	reader := bufio.NewReader(resp.Body)
	prefix, first, prefixErr := readResponsePrefix(reader)
	if prefixErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("failed to read response: %w", prefixErr)
	}
	if first == '{' {
		responseBody := io.MultiReader(bytes.NewReader(prefix), reader)
		body, readErr := io.ReadAll(io.LimitReader(responseBody, 8<<20))
		if readErr != nil {
			return fmt.Errorf("failed to read response: %w", readErr)
		}
		var chatResp structs.ChatCompletionResponse
		if err := json.Unmarshal(body, &chatResp); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
		if err := restoreChatResponse(&chatResp, masker); err != nil {
			return err
		}
		if err := callback(chatResp); err != nil {
			return fmt.Errorf("callback error: %w", err)
		}
		if masker != nil {
			content, reasoning := masker.FinishRestore()
			if content != "" || reasoning != "" {
				var reasoningPtr *string
				if reasoning != "" {
					reasoningPtr = &reasoning
				}
				if err := callback(structs.ChatCompletionResponse{
					Choices: []structs.Choice{{Delta: structs.Message{
						Content: content, ReasoningContent: reasoningPtr,
					}}},
				}); err != nil {
					return fmt.Errorf("callback error: %w", err)
				}
			}
		}
		return nil
	}

	// The prefix contains the first SSE line's bytes. Re-inject it so the normal
	// line reader handles both a no-newline prefix and arbitrary leading whitespace.
	sseReader := bufio.NewReader(io.MultiReader(bytes.NewReader(prefix), reader))
	var dataLines []string
	streamDone := false
	seenSSE := false
	dispatch := func() error {
		if len(dataLines) == 0 {
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		if data == SSEDoneMarker {
			streamDone = true
			return nil
		}
		var raw struct {
			Error *structs.APIError `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &raw); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
		if raw.Error != nil && raw.Error.Message != "" {
			return fmt.Errorf("API error: %s", raw.Error.Message)
		}
		var chatResp structs.ChatCompletionResponse
		if err := json.Unmarshal([]byte(data), &chatResp); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
		if err := restoreChatResponse(&chatResp, masker); err != nil {
			return err
		}
		if err := callback(chatResp); err != nil {
			return fmt.Errorf("callback error: %w", err)
		}
		return nil
	}
	for !streamDone {
		line, err := sseReader.ReadString('\n')
		if err != nil && err != io.EOF {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("failed to read response: %w", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
		} else if strings.HasPrefix(line, ":") {
			// SSE comment/heartbeat.
		} else if after, ok := strings.CutPrefix(line, "data:"); ok {
			seenSSE = true
			if strings.HasPrefix(after, " ") {
				after = after[1:]
			}
			dataLines = append(dataLines, after)
		}
		if err == io.EOF {
			if err := dispatch(); err != nil {
				return err
			}
			break
		}
	}
	if !streamDone && seenSSE {
		return io.ErrUnexpectedEOF
	}
	if !streamDone {
		return fmt.Errorf("invalid empty response")
	}

	// 正常结束时刷出还原器的残留缓冲（未匹配的有界缓冲也是响应文本的一部分）
	if masker != nil {
		if c, r := masker.FinishRestore(); c != "" || r != "" {
			var rp *string
			if r != "" {
				rp = &r
			}
			if err := callback(structs.ChatCompletionResponse{
				Choices: []structs.Choice{{Delta: structs.Message{Content: c, ReasoningContent: rp}}},
			}); err != nil {
				logger.Error("call openai chat error when callback finish: %v", err)
				return fmt.Errorf("callback error: %w", err)
			}
		}
	}

	return nil
}

// readResponsePrefix consumes BOM and JSON whitespace while retaining the bytes
// for the parser. This lets us distinguish ordinary JSON from an SSE stream.
func readResponsePrefix(reader *bufio.Reader) (prefix []byte, first byte, err error) {
	b, err := reader.ReadByte()
	if err != nil {
		return nil, 0, err
	}
	if b == 0xef {
		b2, err2 := reader.ReadByte()
		b3, err3 := reader.ReadByte()
		if err2 != nil || err3 != nil || b2 != 0xbb || b3 != 0xbf {
			return nil, 0, fmt.Errorf("invalid response prefix")
		}
	} else {
		prefix = append(prefix, b)
	}
	for {
		b, err = reader.ReadByte()
		if err != nil {
			return nil, 0, err
		}
		prefix = append(prefix, b)
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return prefix, b, nil
		}
	}
}

func restoreChatResponse(chatResp *structs.ChatCompletionResponse, masker *mask.Engine) error {
	if masker == nil {
		return nil
	}
	for i := range chatResp.Choices {
		choice := &chatResp.Choices[i]
		restoreMessage := func(message *structs.Message) {
			message.Content = masker.RestoreContent(message.Content)
			if rc := message.ReasoningContent; rc != nil {
				r := masker.RestoreReasoning(*rc)
				message.ReasoningContent = &r
			}
			for j := range message.ToolCalls {
				if message.ToolCalls[j].Function != nil {
					message.ToolCalls[j].Function.Arguments = masker.RestoreContent(message.ToolCalls[j].Function.Arguments)
				}
			}
		}
		restoreMessage(&choice.Delta)
		restoreMessage(&choice.Message)
	}
	return nil
}

// SimpleOpenAIEmbedding 发送 OpenAI Embedding 请求
func SimpleOpenAIEmbedding(ctx context.Context, baseURL, apiKey, model string, body structs.EmbeddingRequest) ([][]float32, error) {
	resp, err := SimpleOpenAIEmbeddingResponse(ctx, baseURL, apiKey, model, body)
	if err != nil {
		return nil, err
	}
	embeddings := make([][]float32, len(resp.Data))
	for i, emb := range resp.Data {
		embeddings[i] = emb.Embedding
	}
	return embeddings, nil
}

// SimpleOpenAIEmbeddingResponse 发送请求并保留完整的 embedding 响应（包括 usage）。
func SimpleOpenAIEmbeddingResponse(ctx context.Context, baseURL, apiKey, model string, body structs.EmbeddingRequest) (structs.EmbeddingResponse, error) {
	var zero structs.EmbeddingResponse
	baseURL = strings.TrimRight(baseURL, "/")
	if body.Model == "" {
		body.Model = model
	}
	logger.Info("call openai embedding: %s", baseURL+EmbeddingsEndpoint)

	// 序列化请求体
	payload, err := json.Marshal(body)
	if err != nil {
		logger.Error("call openai embedding error when marshal: %v", err)
		return zero, fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+EmbeddingsEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		logger.Error("call openai embedding error when create request: %v", err)
		return zero, fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", product.UserAgent)

	// 发送请求
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error("call openai embedding error when call: %v", err)
		return zero, fmt.Errorf("failed to send request when call: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体（限制 8MiB 上限，防止异常服务端无限输出导致内存耗尽）
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		logger.Error("call openai embedding error when read response body: %v", err)
		return zero, fmt.Errorf("failed to read response body: %w", err)
	}

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK {
		var errResp structs.ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			logger.Error("call openai embedding error when unmarshal: %v", err)
			return zero, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		logger.Error("call openai embedding error: %s", errResp.Error.Message)
		return zero, fmt.Errorf("API error: %s", errResp.Error.Message)
	}

	// 解析响应
	var embeddingResp structs.EmbeddingResponse
	if err := json.Unmarshal(respBody, &embeddingResp); err != nil {
		logger.Error("call openai embedding error when unmarshal response: %v", err)
		return zero, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// 提取嵌入向量
	embeddings := make([][]float32, len(embeddingResp.Data))
	for i, emb := range embeddingResp.Data {
		embeddings[i] = emb.Embedding
	}

	logger.Info("call openai embedding success, embeddings count: %d", len(embeddingResp.Data))
	return embeddingResp, nil
}
