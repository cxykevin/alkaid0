package structs

import (
	"github.com/cxykevin/alkaid0/config/structs"
	"gorm.io/gorm"
)

// Chats 对话列表
type Chats struct {
	ID          uint32 `gorm:"primaryKey;autoIncrement"`
	LastModelID uint32
	NowAgent    string
	// === 会话过程参数 ===
	DB                   *gorm.DB            `gorm:"-" json:"-"`
	CurrentAgentID       string              `gorm:"-" json:"-"`
	CurrentAgentConfig   structs.AgentConfig `gorm:"-" json:"-"`
	CurrentActivatePath  string              `gorm:"-" json:"-"`
	EnableScopes         map[string]bool     `gorm:"-" json:"-"`
	TemporyDataOfRequest map[string]any      `gorm:"-" json:"-"`
}
