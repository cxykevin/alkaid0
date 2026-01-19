package structs

// Traces 文件跟踪表
type Traces struct {
	Path      string `gorm:"primaryKey"`
	ChatID    uint32 `gorm:"primaryKey"`
	AgentID   string `gorm:"primaryKey"`
	TraceID   uint64
	Chats     Chats     `gorm:"foreignKey:ChatID"`
	SubAgents SubAgents `gorm:"foreignKey:AgentID;constraints:OnDelete:RESTRICT;OnUpdate:CASCADE"`
}
