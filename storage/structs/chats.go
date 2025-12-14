package structs

// Chats 对话列表
type Chats struct {
	ID      uint32 `gorm:"primaryKey;autoIncrement"`
	Summary string
}
