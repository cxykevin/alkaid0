package structs

import (
	"context"
	"maps"
	"sync"
	"time"

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
	Title           string // 用户设置的标题（/title 命令写入）
	AITitle         string // AI 生成的标题（自动生成/compress 重生成写入）
	ReasoningEffort string
	Task            string // 任务计划（markdown 列表，@task 虚拟对象编辑，持久化到 chats 表）
	// UpdatedAt 最后活动时间（GORM autoUpdateTime 约定自动维护）。
	// 会话任何落库变更（消息写入、标题更新等）都会刷新它，用于 session/list 按活动时间倒序展示。
	UpdatedAt time.Time
	// === 会话过程参数 ===
	// agentLifecycleMu 串行化激活/停用，避免并发工具调用重复改变会话状态。
	agentLifecycleMu         sync.Mutex          `gorm:"-" json:"-"`
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
	// ToolCallingStreaming 标记每个工具调用 id 是否为流式增量预览（true）还是最终状态（false）。
	// OnHook 写入时按 session.State 判定：StateReciving/StateRequesting（AI 正在生成）→ 增量；
	// StateToolCalling（审批后执行）→ 最终。SetCallback 据此选事件名。
	ToolCallingStreaming map[string]bool `gorm:"-" json:"-"`
	// toolCtxMu 保护 ToolCallingContext/ToolCallingType/Latest*/ToolCallingStreaming 的并发访问。
	// 流式解析阶段 OnHook（loop 主 goroutine 的 solveFunc）写、SetCallback goroutine 读，
	// 无锁会触发 Go runtime 的 concurrent map read and map write panic。
	toolCtxMu sync.RWMutex `gorm:"-" json:"-"`
	// toolKillMu 保护 ToolKillFn 的并发访问
	toolKillMu sync.Mutex `gorm:"-" json:"-"`
	// ToolKillFn 由当前正在执行的工具注册，loop.Stop() 调用它来中断工具
	ToolKillFn func() `gorm:"-" json:"-"`
	// planPushMu 保护 PlanPushFn 的并发访问
	planPushMu sync.RWMutex `gorm:"-" json:"-"`
	// PlanPushFn 注册 ACP plan 推送回调（server 层在 loadSession 时注册）。
	// task 工具每次修改 @task 后调用 PushPlan，向会话所有客户端广播完整 plan 列表。
	PlanPushFn func(entries []PlanEntry) `gorm:"-" json:"-"`
}

// PlanEntry ACP plan 更新条目（session/update 通知中 update.sessionUpdate="plan"）。
// ACP 规范：每次推送完整列表，客户端整体替换当前 plan。
type PlanEntry struct {
	Content  string `json:"content"`  // 人类可读描述，此处只展示 taskName（嵌套带缩进）
	Priority string `json:"priority"` // high | medium | low，此处固定 "medium"
	Status   string `json:"status"`   // pending | in_progress | completed
}

// AgentLifecycleLock 串行化子代理激活/停用操作。
func (c *Chats) AgentLifecycleLock() {
	if c == nil {
		return
	}
	c.agentLifecycleMu.Lock()
}

// AgentLifecycleUnlock 释放子代理激活/停用操作锁。
func (c *Chats) AgentLifecycleUnlock() {
	if c == nil {
		return
	}
	c.agentLifecycleMu.Unlock()
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

// SetToolKillFn 注册一个函数，用于在 Stop() 被调用时中断当前正在执行的工具。
// 工具在自己的执行开始时调用此方法注册自己的停止逻辑（如杀死进程），
// 执行结束后调用 SetToolKillFn(nil) 清理。
// 每个工具可以定义"不同的stop"——这是给所有工具统一的中断入口。
func (c *Chats) SetToolKillFn(fn func()) {
	if c == nil {
		return
	}
	c.toolKillMu.Lock()
	defer c.toolKillMu.Unlock()
	c.ToolKillFn = fn
}

// KillTool 调用当前工具注册的停止函数（如果有）。
// 由 loop.Stop() 调用，用于中断正在执行的工具。
func (c *Chats) KillTool() {
	if c == nil {
		return
	}
	c.toolKillMu.Lock()
	fn := c.ToolKillFn
	c.ToolKillFn = nil
	c.toolKillMu.Unlock()
	if fn != nil {
		fn()
	}
}

// SetPlanPushFn 注册 ACP plan 推送回调（server 层在 loadSession 时调用一次）。
// task 工具修改 @task 后通过 PushPlan 调用。
func (c *Chats) SetPlanPushFn(fn func(entries []PlanEntry)) {
	if c == nil {
		return
	}
	c.planPushMu.Lock()
	defer c.planPushMu.Unlock()
	c.PlanPushFn = fn
}

// PushPlan 调用已注册的 ACP plan 推送回调。
// 未注册时静默忽略；锁内只取函数指针，广播（网络 I/O）在锁外执行，避免阻塞 task PostHook。
func (c *Chats) PushPlan(entries []PlanEntry) {
	if c == nil {
		return
	}
	c.planPushMu.RLock()
	fn := c.PlanPushFn
	c.planPushMu.RUnlock()
	if fn != nil {
		fn(entries)
	}
}

// SetToolCalling 线程安全地写入工具调用上下文（工具 OnHook 在流式解析/执行阶段调用）。
// 自动初始化 map，供流式增量预览与最终调用信息广播读取。
// 阶段标记按 session.State 判定：StateReciving/StateRequesting（AI 正在生成工具调用）为流式增量，
// 其余（如 StateToolCalling 审批后执行）为最终状态。
func (c *Chats) SetToolCalling(id string, resp any, typ string) {
	if c == nil {
		return
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	if c.ToolCallingContext == nil {
		c.ToolCallingContext = make(map[string]any)
	}
	if c.ToolCallingType == nil {
		c.ToolCallingType = make(map[string]string)
	}
	if c.ToolCallingStreaming == nil {
		c.ToolCallingStreaming = make(map[string]bool)
	}
	streaming := c.State == state.StateReciving || c.State == state.StateRequesting
	c.ToolCallingContext[id] = resp
	c.ToolCallingType[id] = typ
	c.ToolCallingStreaming[id] = streaming
}

// HasToolCalling 判断当前是否存在待广播的工具调用上下文。
func (c *Chats) HasToolCalling() bool {
	if c == nil {
		return false
	}
	c.toolCtxMu.RLock()
	defer c.toolCtxMu.RUnlock()
	return len(c.ToolCallingContext) != 0
}

// SnapshotToolCalling 在锁内拷贝当前工具调用上下文并清空，返回副本。
// 广播等网络 I/O 应在锁外进行，避免长时间阻塞 OnHook 的写入。
func (c *Chats) SnapshotToolCalling() (map[string]any, map[string]string, map[string]bool) {
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	ctx := make(map[string]any, len(c.ToolCallingContext))
	typ := make(map[string]string, len(c.ToolCallingType))
	streaming := make(map[string]bool, len(c.ToolCallingStreaming))
	maps.Copy(ctx, c.ToolCallingContext)
	maps.Copy(typ, c.ToolCallingType)
	maps.Copy(streaming, c.ToolCallingStreaming)
	c.ToolCallingContext = make(map[string]any)
	c.ToolCallingType = make(map[string]string)
	c.ToolCallingStreaming = make(map[string]bool)
	return ctx, typ, streaming
}

// TakeFinalToolCalling 快照并移除所有最终状态（非流式标记）条目，保留流式条目。
// 供 SetCallback 在任意回调时立即广播审批后/执行后的最终 tool_call，
// 不依赖 session.State 判断（审批后空 AIResponse 与新一轮流式存在 State 竞态）。
func (c *Chats) TakeFinalToolCalling() (map[string]any, map[string]string) {
	if c == nil {
		return nil, nil
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	ctx := make(map[string]any)
	typ := make(map[string]string)
	for id := range c.ToolCallingContext {
		if !c.ToolCallingStreaming[id] {
			ctx[id] = c.ToolCallingContext[id]
			typ[id] = c.ToolCallingType[id]
			delete(c.ToolCallingContext, id)
			delete(c.ToolCallingType, id)
			delete(c.ToolCallingStreaming, id)
		}
	}
	return ctx, typ
}

// TakeStreamingToolCalling 快照并移除所有流式增量（streaming 标记）条目，保留最终条目。
// 供 SetCallback 在限流通过时推送增量预览；限流未通过时跳过（不清空，保留供下个 chunk）。
func (c *Chats) TakeStreamingToolCalling() (map[string]any, map[string]string) {
	if c == nil {
		return nil, nil
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	ctx := make(map[string]any)
	typ := make(map[string]string)
	for id := range c.ToolCallingContext {
		if c.ToolCallingStreaming[id] {
			ctx[id] = c.ToolCallingContext[id]
			typ[id] = c.ToolCallingType[id]
			delete(c.ToolCallingContext, id)
			delete(c.ToolCallingType, id)
			delete(c.ToolCallingStreaming, id)
		}
	}
	return ctx, typ
}

// ClearToolCalling 清空当前工具调用上下文（进 WaitApprove 前防止限流跳过的残留）。
func (c *Chats) ClearToolCalling() {
	if c == nil {
		return
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	c.ToolCallingContext = make(map[string]any)
	c.ToolCallingType = make(map[string]string)
	c.ToolCallingStreaming = make(map[string]bool)
}

// ResetLatest 重置最近一次工具调用快照（审批/拒绝完成、新一轮用户输入前调用）。
func (c *Chats) ResetLatest() {
	if c == nil {
		return
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	c.LatestToolCallingContext = make(map[string]any)
	c.LatestToolCallingType = make(map[string]string)
}

// SetLatest 用给定的上下文快照覆盖最近一次工具调用快照。
func (c *Chats) SetLatest(ctx map[string]any, typ map[string]string) {
	if c == nil {
		return
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	c.LatestToolCallingContext = make(map[string]any)
	c.LatestToolCallingType = make(map[string]string)
	maps.Copy(c.LatestToolCallingContext, ctx)
	maps.Copy(c.LatestToolCallingType, typ)
}

// SnapshotLatest 返回最近一次工具调用快照的副本（供 WaitApprove 文本拼接、SessionLoad 重放）。
func (c *Chats) SnapshotLatest() (map[string]any, map[string]string) {
	if c == nil {
		return nil, nil
	}
	c.toolCtxMu.RLock()
	defer c.toolCtxMu.RUnlock()
	ctx := make(map[string]any, len(c.LatestToolCallingContext))
	typ := make(map[string]string, len(c.LatestToolCallingType))
	maps.Copy(ctx, c.LatestToolCallingContext)
	maps.Copy(typ, c.LatestToolCallingType)
	return ctx, typ
}
