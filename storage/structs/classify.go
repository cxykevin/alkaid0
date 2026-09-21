package structs

// ClassifySegment 分类段信息表
// 记录用户消息经 prompt 分类器分割后的每个段的标签信息。
// prompt: 自然语言指令，log: 日志/堆栈，code: 代码片段
type ClassifySegment struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	ChatID    uint32 `gorm:"index"`
	MessageID uint64 `gorm:"index"`
	Label     string `gorm:"type:text"`
	Text      string `gorm:"type:text"`
	TempPath  string `gorm:"type:text"`
	// Chats 关联会话：此前该表既没有外键也没有任何删除路径，会话删掉后行永久残留
	// （Text 里是整段用户输入的副本，无界增长）。OnDelete:CASCADE 让会话删除时
	// 一并清掉它的分类段。没有外键的历史库由 AutoMigrate 补建约束。
	Chats *Chats `gorm:"foreignKey:ChatID;references:ID;constraint:OnDelete:CASCADE"`
}
