package request

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/request/structs"
)

var logger *log.LogsObj

func init() {
	logger = log.New("request")
}

// SimpleOpenAIRequest 发送 OpenAI ChatCompletion 请求（强制stream=true）
func SimpleOpenAIRequest(baseURL, apiKey, model string, body structs.ChatCompletionRequest, callback func(structs.ChatCompletionResponse) error) error {
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
	req, err := http.NewRequest("POST", baseURL+ChatCompletionsEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		logger.Error("call openai chat error when create request: %v", err)
		logger.Debug("error body: %s", string(payload))
		return fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// 发送请求
	client := &http.Client{Timeout: Timeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("call openai chat error when call: %v", err)
		logger.Debug("error body: %s", string(payload))
		return fmt.Errorf("failed to send request when call: %w", err)
	}
	defer resp.Body.Close()

	// 检查HTTP状态码
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		var errResp structs.ErrorResponse
		if err := json.Unmarshal(respBody, &errResp); err != nil {
			logger.Error("call openai chat error when unmarshal: %v", err)
			logger.Debug("error body: %d: %s", resp.StatusCode, string(respBody))
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}
		logger.Error("call openai chat error when unmarshal: %v", err)
		logger.Debug("error body: %s", errResp.Error.Message)
		return fmt.Errorf("API error: %s", errResp.Error.Message)
	}

	// 读取流式响应
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			logger.Error("call openai chat error when read: %v", err)
			logger.Debug("error body: %s", string(line))
			return fmt.Errorf("failed to read response: %w", err)
		}

		// 跳过空行和注释行
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, ":") {
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
	}

	return nil
}

// SimpleOpenAIEmbedding 发送 OpenAI Embedding 请求
func SimpleOpenAIEmbedding(baseURL, apiKey, model string, body structs.EmbeddingRequest) ([][]float32, error) {
	logger.Info("call openai embedding: %s", baseURL+EmbeddingsEndpoint)

	// 序列化请求体
	payload, err := json.Marshal(body)
	if err != nil {
		logger.Error("call openai embedding error when marshal: %v", err)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest("POST", baseURL+EmbeddingsEndpoint, bytes.NewBuffer(payload))
	if err != nil {
		logger.Error("call openai embedding error when create request: %v", err)
		logger.Debug("error body: %s", string(payload))
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// 发送请求
	client := &http.Client{Timeout: Timeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("call openai embedding error when call: %v", err)
		logger.Debug("error body: %s", string(payload))
		return nil, fmt.Errorf("failed to send request when call: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应体
	respBody, err := io.ReadAll(resp.Body)
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
