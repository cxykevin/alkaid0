package structs

// Chats 对话列表
type Chats struct {
	ID          uint32 `gorm:"primaryKey;autoIncrement"`
	LastModelID uint32
	NowAgent    string
}
