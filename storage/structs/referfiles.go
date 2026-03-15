package structs

// ReferFiles 引用的文件列表
type ReferFiles struct {
	ChatID   uint32 `gorm:"primaryKey"`
	Path     string `gorm:"primaryKey"`
	Content  string `gorm:"type:text"`
	ReadOnly bool
	Chats    Chats `gorm:"foreignKey:ChatID;constraints:OnDelete:RESTRICT;OnUpdate:CASCADE"`
}
