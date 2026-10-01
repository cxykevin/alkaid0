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

// FetchConfig fetch 工具配置
type FetchConfig struct {
	// RewriteHeaders URL 正则表达式 → 请求头映射。
	// AI 调用 fetch 工具时，若 url 匹配某个正则，则注入对应的 Headers；
	// AI 在 headers 参数中显式提供的同名请求头会覆盖注入值。
	RewriteHeaders map[string]map[string]string
}

// AgentsConfig 代理配置结构
type AgentsConfig struct {
	Agents                  map[string]AgentConfig
	IgnoreBuiltinAgents     bool   `default:"false"`
	GlobalPrompt            string `default:""`
	SummaryModel            int32
	TitleModel              int32  // 标题生成模型编号（0 表示回退 DefaultModelID，与 SummaryModel 一致）
	MaxCallCount            int32  `default:"50"`
	DefaultAutoApprove      string `default:"" json:"AutoApprove"` // 全局默认自动批准表达式
	DefaultAutoReject       string `default:"" json:"AutoReject"`  // 全局默认自动拒绝表达式
	IgnoreDefaultRules      bool   `default:"false"`
	DisablePromptPreprocess bool   `default:"false"` // 禁用提示词预处理（prompt分类器）
	UseShell                string `default:""`
	// User 终端任务（run 工具执行的命令）运行使用的操作系统用户。
	// 为空表示使用当前用户（Linux 沙盒内为工作目录属主）。
	// 切换到其它用户需要 root（Linux）/管理员（Windows）权限，切换失败时仅记录警告并回退当前用户。
	User string `default:""`
	// FetchProxy fetch 工具的全局 HTTP 代理地址（支持 http/https/socks5），空为直连
	FetchProxy string `default:""`
	// TerminalEnvs 终端启动时注入的环境变量
	TerminalEnvs   map[string]string
	DisableSandbox bool `default:"false"`
	// Fetch fetch 工具配置
	Fetch FetchConfig
}
