package structs

// Traces 文件跟踪表
type Traces struct {
	Path    string `gorm:"primaryKey"`
	ChatID  uint32 `gorm:"primaryKey"`
	Chats   Chats  `gorm:"foreignKey:ChatID"`
	TraceID uint64
}
