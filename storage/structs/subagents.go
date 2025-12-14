package structs

// SubAgents 子 agents 列表
type SubAgents struct {
	ID          string `gorm:"primaryKey"`
	ChatID      uint32
	AgentID     uint32
	BindPath    string
	Deleted     bool
	LastSummary string
	Chats       Chats `gorm:"foreignKey:ChatID"`
}
