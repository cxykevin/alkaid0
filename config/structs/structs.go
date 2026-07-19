package structs

// Config 主配置结构
type Config struct {
	JSONSchema string `json:"$schema" default:"https://raw.githubusercontent.com/cxykevin/alkaid0/refs/heads/main/docs/schemas/config.json"`
	Version    int32
	Model      ModelsConfig
	Agent      AgentsConfig
	ThemeID    int32
	Server     RPCConfig
	// IgnoreSignals 为 true 时忽略 SIGTERM/SIGINT/SIGQUIT 信号，进程不会因这些信号退出
	IgnoreSignals bool `json:"ignoreSignals" default:"false"`
}
