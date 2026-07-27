package structs

// AgentConfig 单个代理配置结构
type AgentConfig struct {
	Color                 Color  // 展示颜色
	AgentName             string `default:"Agent"`                       // 代理名称
	AgentDescription      string `default:"Default Agent"`               // 代理描述（人类可读）
	AgentPrompt           string `default:"You are a helpful assistant"` // 代理提示（AI完整提示）
	AgentModel            int32  `default:"0"`                           // 代理使用的模型编号
	AgentShortDescription string `default:"A default subagent"`          // 代理简短描述（AI激活）
	AutoApprove           string `default:""`                            // 自动批准表达式
	AutoReject            string `default:""`                            // 自动拒绝表达式
	DisableSandbox        bool   `default:"false"`                       // 禁用沙盒
}

// ContextEngineConfig 上下文搜索引擎配置
type ContextEngineConfig struct {
	// BM25Weight BM25 搜索权重（0~1），向量搜索权重为 1-BM25Weight，默认 0.7
	BM25Weight float64 `json:"bm25Weight" default:"0.7"`
	// VectorMinSimilarity 向量最小余弦相似度保留阈值（0~1）
	// 向量搜索结果中余弦相似度低于此值的项被丢弃，默认 0.5
	VectorMinSimilarity float64 `json:"vectorMinSimilarity" default:"0.5"`
	// BM25RetentionScore BM25 保留阈值
	// BM25 得分高于此值的项被丢弃（BM25 得分越低匹配越好），0 表示不限制
	BM25RetentionScore float64 `json:"bm25RetentionScore" default:"0.0"`
}

// AgentsConfig 代理配置结构
type AgentsConfig struct {
	Agents                  map[string]AgentConfig
	IgnoreBuiltinAgents     bool                `default:"false"`
	GlobalPrompt            string              `default:""`
	SummaryModel            int32
	MaxCallCount            int32               `default:"50"`
	DefaultAutoApprove      string              `default:"" json:"AutoApprove"` // 全局默认自动批准表达式
	DefaultAutoReject       string              `default:"" json:"AutoReject"`  // 全局默认自动拒绝表达式
	IgnoreDefaultRules      bool                `default:"false"`
	DisablePromptPreprocess bool                `default:"false"` // 禁用提示词预处理（prompt分类器）
	UseShell                string              `default:""`
	DisableSandbox          bool                `default:"false"`
	ContextEngine           ContextEngineConfig `json:"contextEngine,omitempty"`
}
