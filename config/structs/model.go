package structs

// ProviderSpecificConfig 特定模型提供方配置结构
type ProviderSpecificConfig struct {
	EnableDeepseekThinking bool `default:"false"`
	EnableReasoningEffort  bool `default:"true"`
	EnableTopP             bool `default:"false"`
	EnableTemperature      bool `default:"false"`
	EnableTopK             bool `default:"false"`
	EnableUsage            bool `default:"true"`
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
	EnableToolCalling      bool                   `default:"false"`                         // 是否启用工具调用（只影响 delta 拼接）
	CompressSize           uint32                 `default:"128000"`                        // 压缩大小
	ProviderSpecificConfig ProviderSpecificConfig // 特定模型提供方配置

}

// ModelsConfig 模型配置结构
type ModelsConfig struct {
	ProviderURL    string                `default:"https://openrouter.com/api/v1"` // 模型提供者URL
	ProviderKey    string                `default:"sk-or-xxx"`                     // 模型提供者Key
	DefaultModelID int32                 `default:"0"`
	Models         map[int32]ModelConfig // 模型列表, value为模型配置
}
