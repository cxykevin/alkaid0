package structs

// ModelConfig 单个模型配置结构
type ModelConfig struct {
	ModelName        string  `default:"Kimi K2 Thinking"` // 模型名称
	ModelID          string  `default:"kimi-k2-thinking"` // 模型ID
	ModelDescription string  `default:""`                 // 模型描述
	ModelAddPrompt   string  `default:""`                 // 模型添加提示
	ModelTopP        float32 `default:"0"`                // 模型TopP，0 代表默认
	ModelTopK        float32 `default:"0"`                // 模型TopP，0 代表默认
	ModelTemperature float32 `default:"0.2"`              // 模型温度
	TokenLimit       int32   `default:"8192"`
}

// ModelsConfig 模型配置结构
type ModelsConfig struct {
	ProviderURL    string                `default:"https://openrouter.com/api/v1"` // 模型提供者URL
	ProviderKey    string                `default:"sk-or-xxx"`                     // 模型提供者Key
	DefaultModelID int32                 `default:"0"`
	Models         map[int32]ModelConfig // 模型列表, value为模型配置
}
