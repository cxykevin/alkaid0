package structs

// Terminals 终端表
type Terminals struct {
	ID      uint32 `gorm:"primaryKey"`
	ChatID  uint32
	Chats   Chats  `gorm:"foreignKey:ChatID"`
	History []byte `gorm:"type:blob"`
	Title   string
}
