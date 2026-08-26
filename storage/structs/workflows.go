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
}
