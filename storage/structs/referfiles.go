package structs

// ReferFiles 引用的文件列表
type ReferFiles struct {
	ChatID   uint32 `gorm:"primaryKey"`
	Path     string `gorm:"primaryKey"`
	Content  string `gorm:"type:text"`
	ReadOnly bool
	// 设计注记：本表与会话之间有意不建立外键/关联——deleteChat 只删会话表、保留聊天
	// 记录（见 ui/funcs.DeleteChat），子表数据既不应阻止会话行删除、也不应被级联删除。
	// 历史库遗留的 fk_refer_files_chats 外键由 storage/migrate.MigrateRemoveChatForeignKeys
	// 一次性移除；全新库不再生成。ChatID 是复合主键的一部分，不再是关系字段。
}
