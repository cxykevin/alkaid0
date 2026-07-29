package structs

// ContextConfig 上下文/集成相关配置
type ContextConfig struct {
	// LSP LSP 客户端配置
	LSP LSPConfig
	// EmbeddingModelID 嵌入模型在 Models 映射中的 key
	EmbeddingModelID int32 `default:"1"`
}
