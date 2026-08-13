package structs

// ModelType 模型类型
type ModelType string

// 模型类型
const (
	ModelTypeLLM       ModelType = ""
	ModelTypeEmbedding ModelType = "embedding"
	ModelTypeRerank    ModelType = "rerank"
)

// ProviderSpecificConfig 特定模型提供方配置结构
type ProviderSpecificConfig struct {
	EnableDeepseekThinking bool   `default:"false"`
	EnableReasoningEffort  bool   `default:"true"`
	EnableTopP             bool   `default:"false"`
	EnableTemperature      bool   `default:"false"`
	EnableTopK             bool   `default:"false"`
	EnableUsage            bool   `default:"true"`
	Dimension              int    `default:"0"`    // Embedding 模型维度, 嵌入模型必填
	ToolPromptEnhance      string `default:"auto"` // 工具提示词增强: auto(默认,启用) / on(强制启用) / off(强制关闭)
	EnableToolCallingCompat bool  `default:"false"` // 历史回放兼容模式：把"一个 assistant 携带多个 tool_calls"的消息拆分为逐条"单 tool_call + 结果"，适配逐条转换 role:tool 消息的 OpenAI→Anthropic 代理（默认关闭）
}

// ModelConfig 单个模型配置结构
type ModelConfig struct {
	ModelName              string                 `default:"Kimi K2 Thinking"`              // 模型名称
	ModelID                string                 `default:"kimi-k2-thinking"`              // 模型ID
	ModelDescription       string                 `default:""`                              // 模型描述
	ModelAddPrompt         string                 `default:""`                              // 模型添加提示
	ModelTopP              float32                `default:"-1"`                            // 模型TopP，-1 代表默认
	ModelTopK              float32                `default:"-1"`                            // 模型TopK，-1 代表默认
	ModelTemperature       float32                `default:"-1"`                            // 模型温度，-1 代表默认
	TokenLimit             int32                  `default:"8192"`                          // 模型Token限制
	ProviderURL            string                 `default:"https://openrouter.com/api/v1"` // 覆写模型提供者URL
	ProviderKey            string                 `default:"sk-or-xxx"`                     // 复写模型提供者Key
	EnableThinking         bool                   `default:"false"`                         // 是否启用思考
	EnableToolCalling      bool                   `default:"true"`                          // 是否启用原生 tools 参数/tool_calls 模式（默认 true = 原生 tool_calls 模式；false = 提示词 <tools> 标签模式，兼容旧配置）
	CompressSize           uint32                 `default:"128000"`                        // 压缩大小
	Hide                   bool                   `default:"false"`                         // 在列表中隐藏
	Type                   ModelType              `default:""`                              // 模型类型
	CachePriceMultiplier   float32                `default:"0.2"`                           // 缓存命中 token 相对输入价格的倍率（仅按模型，用于 trace 保留/破坏缓存成本决策）
	CacheRetentionMinutes  int32                  `default:"180"`                          // 缓存保留时间（分钟），从会话最后活动时间起算，超过则强制清除 trace
	ProviderSpecificConfig ProviderSpecificConfig // 特定模型提供方配置
}

// ModelsConfig 模型配置结构
type ModelsConfig struct {
	ProviderURL    string                `default:"https://openrouter.com/api/v1"` // 模型提供者URL
	ProviderKey    string                `default:"sk-or-xxx"`                     // 模型提供者Key
	DefaultModelID int32                 `default:"0"`
	Models         map[int32]ModelConfig `default:"{}"` // 模型列表, value为模型配置
}
