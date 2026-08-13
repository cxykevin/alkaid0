package structs

// Traces 文件跟踪表
type Traces struct {
	Path    string `gorm:"primaryKey"`
	ChatID  uint32 `gorm:"primaryKey"`
	AgentID string `gorm:"primaryKey"`
	TraceID uint64
	// LastContent 方案2 diff 的「旧端存档」= 上次以完整块注入上下文的文件原始内容（空 = 首次跟踪）。
	// 只在方案1（注入完整块）时推进；方案2（旧块+diff）时不推进，保证下次 diff 旧端稳定、
	// 旧块字节与上次注入一致，前缀缓存不被连续编辑破坏。
	LastContent string
	Chats       *Chats `gorm:"foreignKey:ChatID"`
}
