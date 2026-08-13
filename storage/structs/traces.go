package structs

// Traces 文件跟踪表
type Traces struct {
	Path    string `gorm:"primaryKey"`
	ChatID  uint32 `gorm:"primaryKey"`
	AgentID string `gorm:"primaryKey"`
	TraceID uint64
	// LastContent 上次注入上下文的文件原始内容（空 = 首次跟踪）。
	// 用于文件变更时生成 unified diff 并渲染字节一致的旧内容块，以保住前缀缓存。
	LastContent string
	Chats       *Chats `gorm:"foreignKey:ChatID"`
}
