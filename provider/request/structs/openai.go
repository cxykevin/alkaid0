package structs

import "encoding/json"

// 消息角色常量
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"
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
	Model               string                       `json:"model"`
	Messages            []Message                    `json:"messages"`
	Temperature         *float32                     `json:"temperature,omitempty"`
	TopP                *float32                     `json:"top_p,omitempty"`
	MaxTokens           *int                         `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                         `json:"max_completion_tokens,omitempty"`
	Stop                any                          `json:"stop,omitempty"`
	PresencePenalty     *float32                     `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float32                     `json:"frequency_penalty,omitempty"`
	Seed                *int                         `json:"seed,omitempty"`
	N                   *int                         `json:"n,omitempty"`
	Logprobs            *bool                        `json:"logprobs,omitempty"`
	TopLogprobs         *int                         `json:"top_logprobs,omitempty"`
	ResponseFormat      any                          `json:"response_format,omitempty"`
	User                string                       `json:"user,omitempty"`
	Stream              bool                         `json:"stream"`
	StreamOptions       *ChatCompletionStreamOptions `json:"stream_options"`
	Thinking            *ChatCompletionThinkingType  `json:"thinking"`
	ReasoningEffort     *string                      `json:"reasoning_effort,omitempty"`
	Tools               []Tool                       `json:"tools,omitempty"`
	ToolChoice          any                          `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool                        `json:"parallel_tool_calls,omitempty"`
	// PromptCacheKey 显式缓存路由键（OpenAI 系网关用来把同一会话×同一模型路由到同一缓存分片），
	// 由 ProviderSpecificConfig.EnablePromptCacheKey 打开；值不含任何对话内容。
	PromptCacheKey string `json:"prompt_cache_key,omitempty"`
}

// Tool 请求级工具定义（OpenAI tools 参数）
type Tool struct {
	Type     string       `json:"type"` // 固定 "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction 工具函数定义
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema 对象（避免二次转义）
}

// ToolCallMsg assistant 消息内携带的完整工具调用（历史回放，非流式，无 index）
type ToolCallMsg struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc 工具调用的函数信息
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 对象字符串
}

// StreamToolCall 流式 delta.tool_calls 增量（OpenAI 标准：index + 可选 id/type/function 片段）
type StreamToolCall struct {
	Index    int                 `json:"index,omitempty"` // 省略 0：流式单调用与历史回放（完整格式）均合法
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function *StreamToolCallFunc `json:"function"`
}

// StreamToolCallFunc 流式函数增量
type StreamToolCallFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// PromptCacheControl 显式缓存断点（cache_control），挂在 content block 上。
type PromptCacheControl struct {
	Type string `json:"type"` // 目前仅 "ephemeral"
}

// promptCacheContentBlock 带缓存断点的文本内容块（Anthropic content block 形态）。
type promptCacheContentBlock struct {
	Type         string              `json:"type"`
	Text         string              `json:"text"`
	CacheControl *PromptCacheControl `json:"cache_control,omitempty"`
}

// Message 消息结构体
type Message struct {
	Role             string  `json:"role"` // RoleUser | RoleAssistant | RoleSystem | RoleTool
	Content          string  `json:"content"`
	ReasoningContent *string `json:"reasoning_content,omitempty"`
	// Phase 消息阶段（GPT/Codex 系 API 的 phase 字段：commentary = 过程叙述，final_answer = 最终答复）。
	// 该字段只做原样透传：流式响应里收到的值原样落库，历史回放时原样回传
	// （模型据此判断自己上一轮是"还在干活"还是"已收尾"，丢字段会明显掉性能）。
	// 是否回传由模型级 EnablePhase 控制；空值（供应商不下发）不参与序列化。
	Phase      string           `json:"phase,omitempty"`
	ToolCalls  []StreamToolCall `json:"tool_calls"`             // assistant 消息的 tool_calls（含流式 delta 反序列化目标）
	ToolCallID string           `json:"tool_call_id,omitempty"` // tool 角色结果关联的调用 id
	// CacheControl 显式缓存断点（仅出站设置，由 ProviderSpecificConfig.EnablePromptCacheBreakpoints 打开）：
	// 序列化时把 content 转成带 cache_control 的块数组，字段本身不出现在报文里。
	CacheControl *PromptCacheControl `json:"-"`
}

// MarshalJSON 自定义序列化：
//   - 带 CacheControl 的纯文本消息：content 转成带 cache_control 的块数组（显式缓存断点）；
//   - assistant 消息携带 tool_calls 且正文为空时省略 content 字段：OpenAI 规范允许携带
//     tool_calls 的 assistant 消息不带 content；Anthropic 转换代理会拒绝空 text content
//     block（text content blocks must be non-empty）。此前 Content 无 omitempty，
//     纯工具调用回放会发出 "content":""。
func (m Message) MarshalJSON() ([]byte, error) {
	type messageAlias Message
	if m.CacheControl != nil && m.Content != "" && len(m.ToolCalls) == 0 {
		// 显式缓存断点：cache_control 只能挂在 content block 上，把纯文本 content
		// 转成单元素块数组 [{"type":"text","text":...,"cache_control":{"type":"ephemeral"}}]。
		// 无正文或带 tool_calls 的消息不参与（前者无法挂块，后者块形态与 tool_use 混排会被代理拒绝）。
		type messageWithCacheControl struct {
			messageAlias
			Content []promptCacheContentBlock `json:"content"`
		}
		return json.Marshal(messageWithCacheControl{
			messageAlias: messageAlias(m),
			Content: []promptCacheContentBlock{{
				Type:         "text",
				Text:         m.Content,
				CacheControl: m.CacheControl,
			}},
		})
	}
	if m.Content == "" && len(m.ToolCalls) > 0 {
		// 外层 Content 指针遮蔽内嵌别名的 Content 字段，nil + omitempty 即省略该字段
		type messageWithoutContent struct {
			messageAlias
			Content *string `json:"content,omitempty"`
		}
		return json.Marshal(messageWithoutContent{messageAlias: messageAlias(m)})
	}
	return json.Marshal(messageAlias(m))
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
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage 令牌使用统计
type Usage struct {
	PromptTokens        uint32               `json:"prompt_tokens"`
	CompletionTokens    uint32               `json:"completion_tokens"`
	TotalTokens         uint32               `json:"total_tokens"`
	CachedTokens        uint32               `json:"cached_tokens"`
	DeepseekCachedToken uint32               `json:"prompt_cache_hit_tokens,omitempty"`
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
	BillingUsage        *BillingUsage        `json:"billing_usage,omitempty"`
}

// PromptTokensDetails OpenAI 标准嵌套的输入 token 明细，
// 缓存命中 token 位于 prompt_tokens_details.cached_tokens
// （OpenRouter、claude-code-router 等网关透传 Anthropic usage 时填充此字段）。
type PromptTokensDetails struct {
	CachedTokens uint32 `json:"cached_tokens"`
}

// BillingUsage claude-code-router 等网关透传的 Anthropic 计费明细，
// 缓存命中 token 位于 billing_usage.claude_usage.cache_read_input_tokens。
type BillingUsage struct {
	ClaudeUsage *ClaudeUsage `json:"claude_usage,omitempty"`
}

// ClaudeUsage Anthropic 原生 usage 明细。
type ClaudeUsage struct {
	CacheReadInputTokens uint32 `json:"cache_read_input_tokens"`
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
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ReasoningContent *string          `json:"reasoning_content,omitempty"`
	ToolCalls        []StreamToolCall `json:"tool_calls,omitempty"`
	// Phase 见 Message.Phase（commentary / final_answer），与 Message 保持同形便于两套 delta 结构互换。
	Phase string `json:"phase,omitempty"`
}

// EmbeddingRequest 嵌入请求
type EmbeddingRequest struct {
	Input          []string        `json:"input,omitempty"` // 字符串数组
	InputRaw       json.RawMessage `json:"-"`               // 单字符串或 token ID 输入的原始 JSON
	Model          string          `json:"model"`
	EncodingFormat string          `json:"encoding_format,omitempty"` // float, base64
	Dimensions     *int            `json:"dimensions,omitempty"`
	User           string          `json:"user,omitempty"`
}

func (r EmbeddingRequest) MarshalJSON() ([]byte, error) {
	type embeddingRequest EmbeddingRequest
	v := struct {
		embeddingRequest
		Input json.RawMessage `json:"input"`
	}{embeddingRequest: embeddingRequest(r), Input: r.InputRaw}
	if len(v.Input) == 0 {
		input, err := json.Marshal(r.Input)
		if err != nil {
			return nil, err
		}
		v.Input = input
	}
	return json.Marshal(v)
}

func (r *EmbeddingRequest) UnmarshalJSON(data []byte) error {
	type embeddingRequest EmbeddingRequest
	var v struct {
		Input json.RawMessage `json:"input"`
		*embeddingRequest
	}
	v.embeddingRequest = (*embeddingRequest)(r)
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	r.InputRaw = append(r.InputRaw[:0], v.Input...)
	var input []string
	if err := json.Unmarshal(v.Input, &input); err == nil {
		r.Input = input
	} else {
		r.Input = nil
	}
	return nil
}

// EmbeddingResponse 嵌入响应
type EmbeddingResponse struct {
	Object string      `json:"object"`
	Data   []Embedding `json:"data"`
	Model  string      `json:"model"`
	Usage  *Usage      `json:"usage,omitempty"`
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
