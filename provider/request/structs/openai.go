package structs

// 消息角色常量
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// ChatCompletionThinkingType 设置thinking类型
type ChatCompletionThinkingType struct {
	Type string `json:"type"`
}

// ChatCompletionStreamOptions 流式响应选项
type ChatCompletionStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatCompletionRequest OpenAI ChatCompletion 请求结构体
type ChatCompletionRequest struct {
	Model           string                       `json:"model"`
	Messages        []Message                    `json:"messages"`
	Temperature     *float32                     `json:"temperature,omitempty"`
	TopP            *float32                     `json:"top_p,omitempty"`
	MaxTokens       *int                         `json:"max_tokens,omitempty"`
	User            string                       `json:"user,omitempty"`
	Stream          bool                         `json:"stream"`
	StreamOptions   *ChatCompletionStreamOptions `json:"stream_options,omitempty"`
	Thinking        *ChatCompletionThinkingType  `json:"thinking,omitempty"`         // enabled | disabled
	ReasoningEffort *string                      `json:"reasoning_effort,omitempty"` // low | medium | high | max | xhigh
}

// Message 消息结构体
type Message struct {
	Role             string  `json:"role"` // RoleUser | RoleAssistant | RoleSystem
	Content          string  `json:"content"`
	ReasoningContent *string `json:"reasoning_content,omitempty"`
}

// ChatCompletionResponse OpenAI ChatCompletion 响应结构体
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice 选择项
type Choice struct {
	Index        int     `json:"index"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason"`
}

// Usage 令牌使用统计
type Usage struct {
	PromptTokens        uint32 `json:"prompt_tokens"`
	CompletionTokens    uint32 `json:"completion_tokens"`
	TotalTokens         uint32 `json:"total_tokens"`
	CachedTokens        uint32 `json:"cached_tokens"`
	DeepseekCachedToken uint32 `json:"prompt_cache_hit_tokens,omitempty"`
}

// ChatCompletionStream 流式响应块
type ChatCompletionStream struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// StreamChoice 流式选择项
type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// StreamDelta 流式增量消息
type StreamDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// EmbeddingRequest 嵌入请求
type EmbeddingRequest struct {
	Input          []string `json:"input"` // 字符串数组
	Model          string   `json:"model"`
	EncodingFormat string   `json:"encoding_format,omitempty"` // float, base64
	User           string   `json:"user,omitempty"`
}

// EmbeddingResponse 嵌入响应
type EmbeddingResponse struct {
	Object string      `json:"object"`
	Data   []Embedding `json:"data"`
	Model  string      `json:"model"`
	Usage  Usage       `json:"usage"`
}

// Embedding 单个嵌入
type Embedding struct {
	Object    string    `json:"object"`
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// ErrorResponse API 错误响应
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// APIError API 错误信息
type APIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   any    `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}
