package structs

import "time"

// Workflows stores durable dynworkflow state.
type Workflows struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement"`
	WorkflowID     string `gorm:"uniqueIndex:ux_workflow_chat;not null"`
	ChatID         uint32 `gorm:"uniqueIndex:ux_workflow_chat;index:idx_workflow_chat_status"`
	RunID          string `gorm:"uniqueIndex;not null"`
	TerminalID     string
	Name           string
	Status         string `gorm:"index:idx_workflow_chat_status"`
	CurrentNode    string
	CurrentAgent   string
	GraphJSON      string `gorm:"type:text"`
	AgentStateJSON string `gorm:"type:text"`
	Error          string `gorm:"type:text"`
	ResultPath     string
	LastSequence   uint64
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	UpdatedAt      time.Time
	// 设计注记：本表与会话之间有意不建立外键/关联——deleteChat 只删会话表、保留聊天
	// 记录（见 ui/funcs.DeleteChat），子表数据既不应阻止会话行删除、也不应被级联删除。
	// 此前本表曾以 OnDelete:CASCADE 随会话删除做级联清理，该行为与「只删会话表」的
	// 设计冲突、已废弃；历史库遗留的 fk_workflows_chats 外键由 storage/migrate.
	// MigrateRemoveChatForeignKeys 一次性移除；全新库不再生成。
}

// WorkflowEvents stores ordered workflow events.
type WorkflowEvents struct {
	ID          uint64 `gorm:"primaryKey;autoIncrement"`
	WorkflowID  string `gorm:"uniqueIndex:ux_workflow_event;index"`
	ChatID      uint32 `gorm:"index"`
	Sequence    uint64 `gorm:"uniqueIndex:ux_workflow_event"`
	Type        string `gorm:"index"`
	NodeID      string
	AgentIndex  int
	PayloadJSON string `gorm:"type:text"`
	RawJSON     string `gorm:"type:text"`
	CreatedAt   time.Time
	// 设计注记：与 Workflows 相同——本表与会话之间有意不建立外键/关联，删除会话时
	// 事件数据保留（deleteChat 只删会话表，见 ui/funcs.DeleteChat）；历史库遗留的
	// fk_workflow_events_chats 外键由 storage/migrate.MigrateRemoveChatForeignKeys 移除。
}
