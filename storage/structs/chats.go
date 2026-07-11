package structs

import (
	"context"
	"sync"

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

// contextHolder 包装 RWMutex 用于线程安全的 Context 访问
// 使用指针避免 Chats 值拷贝时复制锁
type contextHolder struct {
	mu  sync.RWMutex
	ctx context.Context
}

// Chats 对话列表
type Chats struct {
	ID              uint32 `gorm:"primaryKey;autoIncrement"`
	LastModelID     uint32
	NowAgent        string
	Root            string
	TraceID         uint64
	State           state.State
	Title           string
	ReasoningEffort string
	// AlivePolicy ChatAlivePolicy
	// === 会话过程参数 ===
	contextHolder            *contextHolder      `gorm:"-" json:"-"`
	Stop                     bool                `gorm:"-" json:"-"`
	DB                       *gorm.DB            `gorm:"-" json:"-"`
	CurrentAgentID           string              `gorm:"-" json:"-"`
	CurrentAgentConfig       structs.AgentConfig `gorm:"-" json:"-"`
	CurrentActivatePath      string              `gorm:"-" json:"-"`
	EnableScopes             map[string]bool     `gorm:"-" json:"-"`
	TemporyDataOfRequest     map[string]any      `gorm:"-" json:"-"`
	TemporyDataOfSession     map[string]any      `gorm:"-" json:"-"`
	InTestFlag               bool                `gorm:"-" json:"-"`
	ReferCount               int32               `gorm:"-" json:"-"`
	ToolCallingContext       map[string]any      `gorm:"-" json:"-"`
	ToolCallingType          map[string]string   `gorm:"-" json:"-"`
	CurrentToolID            string              `gorm:"-" json:"-"`
	CurrentMessageID         uint64              `gorm:"-" json:"-"`
	ToolState                uint64              `gorm:"-" json:"-"`
	LatestToolCallingContext map[string]any      `gorm:"-" json:"-"`
	LatestToolCallingType    map[string]string   `gorm:"-" json:"-"`
}

// SetContext 线程安全地设置会话上下文
func (c *Chats) SetContext(ctx context.Context) {
	if c.contextHolder == nil {
		c.contextHolder = &contextHolder{}
	}
	c.contextHolder.mu.Lock()
	c.contextHolder.ctx = ctx
	c.contextHolder.mu.Unlock()
}

// GetContext 线程安全地获取会话上下文，如果未设置则返回 background
func (c *Chats) GetContext() context.Context {
	if c.contextHolder == nil {
		return context.Background()
	}
	c.contextHolder.mu.RLock()
	defer c.contextHolder.mu.RUnlock()
	if c.contextHolder.ctx == nil {
		return context.Background()
	}
	return c.contextHolder.ctx
}
