package structs

import "time"

// MsgQueueItems 会话消息队列（与 Messages 分开存储）。
//
// 入队时只写本表；真正交给 loop 处理时才写入 Messages 并由服务端广播。
// 因此排队中的消息不会出现在历史回放里，也不会占用主消息表。
//
// 设计注记：本表与会话之间有意不建立外键/关联——队列是运行态数据，
// 删除会话时由 server/actions.SessionDelete 显式清理；聊天历史仍按
// 「只删会话表、保留消息」的既有约定处理。
type MsgQueueItems struct {
	ID     uint64 `gorm:"primaryKey;autoIncrement"`
	ChatID uint32 `gorm:"index:idx_msgqueue_chat_seq"`
	Seq    uint64 `gorm:"index:idx_msgqueue_chat_seq"`
	ItemID string `gorm:"uniqueIndex;not null"`
	// Kind prompt | command
	Kind string `gorm:"index"`
	// Command/Args 仅 command 项使用（如 /compress、/s）。
	Command   string
	Args      string `gorm:"type:text"`
	Text      string `gorm:"type:text"`
	CreatedAt time.Time
}
