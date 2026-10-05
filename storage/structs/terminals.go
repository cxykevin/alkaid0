package structs

// Terminals 终端表
type Terminals struct {
	ID     uint32 `gorm:"primaryKey"`
	ChatID uint32
	// 设计注记：本表与会话之间有意不建立外键/关联——deleteChat 只删会话表、保留聊天
	// 记录（见 ui/funcs.DeleteChat），子表数据既不应阻止会话行删除、也不应被级联删除。
	// 历史库遗留的 fk_terminals_chats 外键由 storage/migrate.MigrateRemoveChatForeignKeys
	// 一次性移除；全新库不再生成。
	History []byte `gorm:"type:blob"`
	Title   string
}
