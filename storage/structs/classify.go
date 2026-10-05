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
	// 设计注记：本表与会话之间有意不建立外键/关联——deleteChat 只删会话表、保留聊天
	// 记录（见 ui/funcs.DeleteChat），子表数据既不应阻止会话行删除、也不应被级联删除。
	// 此前本表曾以 OnDelete:CASCADE 随会话删除做级联清理，该行为与「只删会话表」的
	// 设计冲突、已废弃；历史库遗留的 fk_classify_segments_chats 外键由
	// storage/migrate.MigrateRemoveChatForeignKeys 一次性移除；全新库不再生成。
}
