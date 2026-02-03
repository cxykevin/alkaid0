package structs

// Scopes 存储命名空间启用状态
type Scopes struct {
	ChatID  uint32 `gorm:"primaryKey;column:chat_id"`
	Name    string `gorm:"primaryKey;column:name"`
	Enabled bool   `gorm:"column:enabled"`
	Chats   Chats  `gorm:"foreignKey:ChatID;references:ID"`
}
