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

// SimpleOpenAIRequest 发送 OpenAI ChatCompletion 请求（强制stream=true）
func SimpleOpenAIRequest(ctx context.Context, baseURL, apiKey, model string, body structs.ChatCompletionRequest, callback func(structs.ChatCompletionResponse) error) error {
	// 设置模型和流式参数
	if body.Model == "" {
		body.Model = model
	}
	body.Stream = true

	logger.Info("call openai chat: %s", baseURL+ChatCompletionsEndpoint)

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
		logger.Debug("error body: %s", string(payload))
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
		logger.Debug("error body: %s", string(payload))
		return fmt.Errorf("failed to send request when call: %w", err)
	}
	defer resp.Body.Close()

	// 当 context 被取消时关闭 response body，以中断阻塞的 SSE 读取。
	// Go 的 http.Client 在请求发送后的 body 读取阶段不会检查 context，
	// 因此需要在单独的 goroutine 中监听取消信号并主动关闭连接。
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			resp.Body.Close()
		case <-done:
		}
	}()

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK {
		// 限制错误响应体大小，防止恶意/异常服务端发送无限响应体导致 OOM
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var errResp structs.ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			logger.Error("call openai chat error when unmarshal: %v", err)
			logger.Debug("error body: %d: %s", resp.StatusCode, string(respBody))
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}
		logger.Error("call openai chat error when check stat %v", resp.StatusCode)
		logger.Debug("error body: %s", errResp.Error.Message)
		return fmt.Errorf("API error: %d %s", resp.StatusCode, errResp.Error.Message)
	}

	// 读取流式响应
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			// 如果 context 已被取消（如用户调用了 cancel），返回 context 错误而非读取错误
			if ctx.Err() != nil {
				return ctx.Err()
			}
			logger.Error("call openai chat error when read: %v", err)
			logger.Debug("error body: %s", string(line))
			return fmt.Errorf("failed to read response: %w", err)
		}

		// 跳过空行和注释行
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, ":") {
			if err == io.EOF {
				break
			}
			continue
		}

		// 解析data:开头的行
		if after, ok := strings.CutPrefix(line, SSEDataPrefix); ok {
			data := strings.TrimSpace(after)

			// 检查是否是结束标记
			if data == SSEDoneMarker {
				break
			}

			// 解析响应
			var chatResp structs.ChatCompletionResponse
			if err := json.Unmarshal([]byte(data), &chatResp); err != nil {
				logger.Error("call openai chat error when unmarshal: %v", err)
				logger.Debug("error body: %s", string(data))
				return fmt.Errorf("failed to unmarshal response: %w", err)
			}

			// 调用回调函数处理响应
			if err := callback(chatResp); err != nil {
				logger.Error("call openai chat error when callback: %v", err)
				return fmt.Errorf("callback error: %w", err)
			}
		}

		// EOF：即使最后一行无换行结尾也已在上方处理，正常退出循环
		if err == io.EOF {
			break
		}
	}

	return nil
}

// SimpleOpenAIEmbedding 发送 OpenAI Embedding 请求
func SimpleOpenAIEmbedding(ctx context.Context, baseURL, apiKey, model string, body structs.EmbeddingRequest) ([][]float32, error) {
	logger.Info("call openai embedding: %s", baseURL+EmbeddingsEndpoint)

	// 序列化请求体
	payload, err := json.Marshal(body)
	if err != nil {
		logger.Error("call openai embedding error when marshal: %v", err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+EmbeddingsEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		logger.Error("call openai embedding error when create request: %v", err)
		logger.Debug("error body: %s", string(payload))
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", product.UserAgent)

	// 发送请求
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error("call openai embedding error when call: %v", err)
		logger.Debug("error body: %s", string(payload))
		return nil, fmt.Errorf("failed to send request when call: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体（限制 8MiB 上限，防止异常服务端无限输出导致内存耗尽）
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		logger.Error("call openai embedding error when read response body: %v", err)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK {
		var errResp structs.ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			logger.Error("call openai embedding error when unmarshal: %v", err)
			logger.Debug("error body: %d: %s", resp.StatusCode, string(respBody))
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}
		logger.Error("call openai embedding error: %s", errResp.Error.Message)
		return nil, fmt.Errorf("API error: %s", errResp.Error.Message)
	}

	// 解析响应
	var embeddingResp structs.EmbeddingResponse
	if err := json.Unmarshal(respBody, &embeddingResp); err != nil {
		logger.Error("call openai embedding error when unmarshal response: %v", err)
		logger.Debug("error body: %s", string(respBody))
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// 提取嵌入向量
	embeddings := make([][]float32, len(embeddingResp.Data))
	for i, emb := range embeddingResp.Data {
		embeddings[i] = emb.Embedding
	}

	logger.Info("call openai embedding success, embeddings count: %d", len(embeddings))
	return embeddings, nil
}
