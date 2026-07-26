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
}
