package structs

// AgentConfig 单个代理配置结构
type AgentConfig struct {
	Color                 Color  // 展示颜色
	AgentName             string `default:"Agent"`                       // 代理名称
	AgentDescription      string `default:"Default Agent"`               // 代理描述（人类可读）
	AgentPrompt           string `default:"You are a helpful assistant"` // 代理提示（AI完整提示）
	AgentModel            int32  `default:"0"`                           // 代理使用的模型编号
	AgentShortDescription string `default:"A default subagent"`          // 代理简短描述（AI激活）
}

// AgentsConfig 代理配置结构
type AgentsConfig struct {
	Agents       map[string]AgentConfig
	GlobalPrompt string `default:""`
	SummaryModel int32
	MaxCallCount int32 `default:"50"`
}
