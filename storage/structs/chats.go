package structs

import (
	"context"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	"gorm.io/gorm"
)

// // ChatAlivePolicy 对话存活策略
// type ChatAlivePolicy uint16

// // 存活策略枚举
// const (
// 	ChatAlivePolicyExitOnClose ChatAlivePolicy = iota
// 	ChatAlivePolicyExitOnStop
// )

// Chats 对话列表
type Chats struct {
	ID          uint32 `gorm:"primaryKey;autoIncrement"`
	LastModelID uint32
	NowAgent    string
	Root        string
	TraceID     uint64
	State       state.State
	Title       string
	// AlivePolicy ChatAlivePolicy
	// === 会话过程参数 ===
	Context              *context.Context    `gorm:"-" json:"-"`
	Stop                 bool                `gorm:"-" json:"-"`
	DB                   *gorm.DB            `gorm:"-" json:"-"`
	CurrentAgentID       string              `gorm:"-" json:"-"`
	CurrentAgentConfig   structs.AgentConfig `gorm:"-" json:"-"`
	CurrentActivatePath  string              `gorm:"-" json:"-"`
	EnableScopes         map[string]bool     `gorm:"-" json:"-"`
	TemporyDataOfRequest map[string]any      `gorm:"-" json:"-"`
	TemporyDataOfSession map[string]any      `gorm:"-" json:"-"`
	InTestFlag           bool                `gorm:"-" json:"-"`
	ReferCount           int32               `gorm:"-" json:"-"`
	ToolCallingContext   map[string]any      `gorm:"-" json:"-"`
	ToolCallingType      map[string]string   `gorm:"-" json:"-"`
	CurrentToolID        string              `gorm:"-" json:"-"`
	CurrentMessageID     uint64              `gorm:"-" json:"-"`
	ToolState            uint64              `gorm:"-" json:"-"`
}
