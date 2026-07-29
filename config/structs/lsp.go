package structs

// LSPConfig LSP 客户端配置
type LSPConfig struct {
	// Enabled 是否启用 LSP 客户端
	Enabled bool `default:"false"`
	// LanguageServers 自定义语言服务器映射，key 为文件扩展名（如 ".go"）
	// 未配置的扩展名将使用内置默认值
	LanguageServers map[string]LanguageServerConfig
	// IdleTimeout 空闲超时秒数，超过此时间未使用的 LSP 进程将被回收
	IdleTimeout int32 `default:"600"`
}

// LanguageServerConfig 单个语言服务器的配置
type LanguageServerConfig struct {
	// Command 可执行文件路径或命令名
	Command string
	// Args 命令行参数
	Args []string
}
