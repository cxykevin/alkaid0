package request

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/provider/storage"
)

// Timeout 超时
const Timeout = 120 * time.Second

// OpenAIRequest OpenAI格式的请求结构
type OpenAIRequest struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float32         `json:"temperature,omitempty"`
	TopP        float32         `json:"top_p,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []OpenAITool    `json:"tools,omitempty"`
	ToolChoice  string          `json:"tool_choice,omitempty"`
}

// OpenAIMessage OpenAI格式的消息结构
type OpenAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// OpenAITool OpenAI格式的工具定义
type OpenAITool struct {
	Type     string             `json:"type"`
	Function OpenAIToolFunction `json:"function"`
}

// OpenAIToolFunction OpenAI格式的工具函数定义
type OpenAIToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// OpenAIToolCall OpenAI格式的工具调用
type OpenAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function OpenAIToolFunctionCall `json:"function"`
}

// OpenAIToolFunctionCall OpenAI格式的工具函数调用
type OpenAIToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// OpenAIResponse OpenAI格式的响应结构
type OpenAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   OpenAIUsage    `json:"usage"`
}

// OpenAIChoice OpenAI格式的选择
type OpenAIChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// OpenAIUsage OpenAI格式的使用统计
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIStreamResponse OpenAI格式的流式响应结构
type OpenAIStreamResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
}

// StreamChoice 流式响应的选择
type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
}

// StreamDelta 流式响应的增量内容
type StreamDelta struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

var ctx context.Context
var closeCtxFunc context.CancelFunc

// Close 关闭请求
func Close() bool {
	select {
	case <-ctx.Done():
		closeCtxFunc()
		<-ctx.Done()
		return true
	default:
		return false
	}
}

// Request 发送OpenAI格式的请求
func Request(messages *storage.ChatHistory, callback func(*storage.ChatMessageObject) error) error {
	Close()
	// 修复：正确接收 WithTimeout 的 cancel 函数，避免与 err 混淆
	ctx, closeCtxFunc = context.WithTimeout(context.Background(), Timeout)
	defer closeCtxFunc()

	// 构建OpenAI格式的消息
	openAIMessages := make([]OpenAIMessage, len(messages.Content))
	for i, msg := range messages.Content {
		role := ""
		switch msg.Type {
		case storage.ChatTypeUser:
			role = "user"
		case storage.ChatTypeAssistant:
			role = "assistant"
		case storage.ChatTypeToolCalling:
			role = "tool_calling"
		}

		openAIMessages[i] = OpenAIMessage{
			Role:    role,
			Content: msg.Message,
		}
	}

	// 构建OpenAI格式的请求
	req := OpenAIRequest{
		Model:       config.GlobalConfig.Model.Models[config.GlobalConfig.Model.DefaultModelID].ModelID,
		Messages:    openAIMessages,
		MaxTokens:   int(config.GlobalConfig.Model.Models[config.GlobalConfig.Model.DefaultModelID].TokenLimit),
		Temperature: config.GlobalConfig.Model.Models[config.GlobalConfig.Model.DefaultModelID].ModelTemperature,
		TopP:        config.GlobalConfig.Model.Models[config.GlobalConfig.Model.DefaultModelID].ModelTopP,
		Stream:      true,
	}

	// 将请求转换为JSON
	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建HTTP请求
	httpReq, err := http.NewRequest("POST", config.GlobalConfig.Model.ProviderURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	// 将请求与上下文关联并重新赋值
	httpReq = httpReq.WithContext(ctx)

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+config.GlobalConfig.Model.ProviderKey)

	// 创建HTTP客户端
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// 发送请求
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 处理流式响应
	reader := resp.Body
	buffer := make([]byte, 0, 4096)

	msgID := messages.LastID
	messages.LastID++
	// 在history中添加一条记录
	msgObj := &storage.ChatMessageObject{
		ID:        msgID,
		Type:      storage.ChatTypeAssistant,
		Model:     req.Model,
		ModelName: config.GlobalConfig.Model.Models[config.GlobalConfig.Model.DefaultModelID].ModelName,
	}
	messages.Content = append(messages.Content, msgObj)

	for {
		// 检查上下文是否已取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 读取一块数据
		n, err := reader.Read(buffer[len(buffer):cap(buffer)])
		if n > 0 {
			buffer = buffer[:len(buffer)+n]
		}

		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading response: %w", err)
		}

		// 处理完整的行
		for {
			lineEnd := bytes.IndexByte(buffer, '\n')
			if lineEnd == -1 {
				break
			}

			line := buffer[:lineEnd]
			buffer = buffer[lineEnd+1:]

			// 跳过空行
			if len(line) == 0 || (len(line) == 1 && line[0] == '\r') {
				continue
			}

			// 解析SSE数据行
			if bytes.HasPrefix(line, []byte("data: ")) {
				data := line[6:] // 去掉 "data: " 前缀

				// 检查是否是结束标志
				if bytes.Equal(data, []byte("[DONE]")) {
					return nil
				}

				// 解析JSON响应
				var streamResp OpenAIStreamResponse
				if err := json.Unmarshal(data, &streamResp); err != nil {
					continue // 跳过无法解析的行
				}

				// 处理流式响应数据
				if len(streamResp.Choices) > 0 && len(streamResp.Choices[0].Delta.Content) > 0 {
					chatMsg := &storage.ChatMessageObject{
						Reasoning: streamResp.Choices[0].Delta.ReasoningContent,
						Message:   streamResp.Choices[0].Delta.Content,
						Type:      storage.ChatTypeAssistant,
						Model:     streamResp.Model,
						ModelName: config.GlobalConfig.Model.Models[config.GlobalConfig.Model.DefaultModelID].ModelName,
					}
					if streamResp.Choices[0].Delta.ReasoningContent != "" {
						chatMsg.Message = streamResp.Choices[0].Delta.ReasoningContent
					}

					if err := callback(chatMsg); err != nil {
						return fmt.Errorf("callback error: %w", err)
					}
				}
			}
		}
	}

	return nil
}

// OpenAIChoiceDelta OpenAI格式的流式选择
type OpenAIChoiceDelta struct {
	Index        int                `json:"index"`
	Delta        OpenAIMessageDelta `json:"delta"`
	FinishReason string             `json:"finish_reason,omitempty"`
}

// OpenAIMessageDelta OpenAI格式的流式消息
type OpenAIMessageDelta struct {
	Role             string `json:"role,omitempty"`
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}
