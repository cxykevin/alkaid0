package structs

import (
	"context"
	"maps"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/ui/state"
	"gorm.io/gorm"
)

var logger = log.New("storage")

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
	Hidden          bool   `gorm:"not null;default:false"`
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
	SystemPrompt             string              `gorm:"-" json:"-"`
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
	ToolCallingStreaming map[string]bool   `gorm:"-" json:"-"`
	ToolCallingRunID     map[string]string `gorm:"-" json:"-"`
	// ToolCallingRawParams 记录每个工具调用的**原始参数**，仅用于把展示 content 规范化成
	// "完整参数"渲染（文本块与 calling_info.args），不对外发送（协议里没有 rawInput 字段）。
	// 由 tools.ExecToolOnHook 在 OnHook 前按 CurrentToolID 写入；未取出的条目在
	// ClearToolCalling 时清理。
	ToolCallingRawParams map[string]any `gorm:"-" json:"-"`
	// ToolCallingTerminalID 记录每个工具调用对应的终端 ID（统一为 @temp/run/<n>），
	// 随 tool_call_update 顶层 alk.cxykevin.top/terminal_id 广播，
	// 客户端据此在终端结束后取回该终端的持久化内容。
	ToolCallingTerminalID map[string]string `gorm:"-" json:"-"`
	// toolCtxMu 保护 ToolCallingContext/ToolCallingType/Latest*/ToolCallingStreaming 的并发访问。
	// 流式解析阶段 OnHook（loop 主 goroutine 的 solveFunc）写、SetCallback goroutine 读，
	// 无锁会触发 Go runtime 的 concurrent map read and map write panic。
	toolCtxMu sync.RWMutex `gorm:"-" json:"-"`
	// runtimeStateMu 保护 ToolState / CurrentMessageID 的并发访问。
	// 两者是 uint64：32 位平台上结构体字段不保证 8 字节对齐，无法安全地就地做原子
	// 64 位操作，故用 RWMutex；State 是 int32，访问器直接对原字段做原子 32 位读写。
	runtimeStateMu sync.RWMutex `gorm:"-" json:"-"`
	// toolKillMu 保护 ToolKillFn 的并发访问
	toolKillMu sync.Mutex `gorm:"-" json:"-"`
	// ToolKillFn 由当前正在执行的工具注册，loop.Stop() 调用它来中断工具
	ToolKillFn func() `gorm:"-" json:"-"`
	// planPushMu 保护 PlanPushFn 的并发访问
	planPushMu sync.RWMutex `gorm:"-" json:"-"`
	// PlanPushFn 注册 ACP plan 推送回调（server 层在 loadSession 时注册）。
	// task 工具每次修改 @task 后调用 PushPlan，向会话所有客户端广播完整 plan 列表。
	PlanPushFn func(entries []PlanEntry) `gorm:"-" json:"-"`
	// TerminalPushFn broadcasts a full active-terminal snapshot after background updates.
	TerminalPushFn  func(terminalID, status, content string) `gorm:"-" json:"-"`
	WorkflowEventFn func(runID string, event any)            `gorm:"-" json:"-"`
	ShellStopFn     func(runID, command string, result any)  `gorm:"-" json:"-"`
	// WorkflowStopFn workflow 终端结束回调（runID、终态、最终结果），用于终态落库。
	WorkflowStopFn func(runID string, status string, result any) `gorm:"-" json:"-"`
}

// PlanEntry ACP plan 更新条目（session/update 通知中 update.sessionUpdate="plan"）。
// ACP 规范：每次推送完整列表，客户端整体替换当前 plan。
type PlanEntry struct {
	Content  string `json:"content"`  // 人类可读描述，此处只展示 taskName（嵌套带缩进）
	Priority string `json:"priority"` // high | medium | low，此处固定 "medium"
	Status   string `json:"status"`   // pending | in_progress | completed
}

// EffectiveModelID 返回本次请求应使用的模型 ID：
// 活跃子代理配置了模型时优先用它，否则用会话最后选择的模型。
//
// 这是**唯一**的模型解析入口：此前这段判断在 5 处各抄了一份
// （request.SendRequest、build.Build、ui/loop、run/python、actions.currentTokenLimit），
// 其中 build.Build 漏了子代理分支，导致请求体里的 model 与参数取自父模型，
// 而连接目标/计费/统计却按子代理模型走——轻则参数不符，重则直接 404。
func (c *Chats) EffectiveModelID() uint32 {
	if c == nil {
		return 0
	}
	if c.CurrentAgentID != "" {
		if id := uint32(c.CurrentAgentConfig.AgentModel); id != 0 {
			return id
		}
	}
	return c.LastModelID
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

// === 会话状态访问器（P1-18 / F4） ===
//
// State 是持久化列（chats.state，SendRequest/ExecuteToolCalls 依赖它做断点恢复与
// 客户端状态机同步），不能改成 atomic 类型字段——server/actions 等处大量
// `sess.State == state.StateIdle` 的直接比较会编译失败，GORM 也需要它是普通字段。
// 因此这里保持字段本身不变，只把读写改为对同一块内存的原子操作：
//   - State 底层是 int32，在 Go 支持的所有平台上都满足原子对齐要求；
//   - ToolState / CurrentMessageID 是 uint64，32 位平台上字段可能只有 4 字节对齐
//     （对非对齐地址做 64 位原子操作会 panic），故用 runtimeStateMu 保护。
//
// 为什么必须全部读点一起改：Go race detector 把「原子写 + 普通读」同样判定为
// data race（实测 atomic.StoreInt32 与普通读并发会报 WARNING: DATA RACE），
// 所以只要还有一个裸读，-race 就仍会失败。ui/loop 与 provider/request 已全部改走
// 访问器；server/actions、ui/funcs 等目录的读点见 P1-18 报告清单。

// atomicStatePtr 返回 State 字段的 int32 原子视图。
// 与标准库 sync/atomic.Int32 的实现同理（对字段地址做 32 位原子指令），
// 保证不影响 GORM 持久化与既有直接比较的调用点。
func (c *Chats) atomicStatePtr() *int32 {
	return (*int32)(unsafe.Pointer(&c.State))
}

// GetState 原子读取会话状态。
func (c *Chats) GetState() state.State {
	if c == nil {
		return state.StateIdle
	}
	return state.State(atomic.LoadInt32(c.atomicStatePtr()))
}

// SetState 原子写入会话状态（不落库；需要持久化时用 SaveState）。
func (c *Chats) SetState(s state.State) {
	if c == nil {
		return
	}
	atomic.StoreInt32(c.atomicStatePtr(), int32(s))
}

// SaveState 原子写入会话状态并以单列 UPDATE 落库。
// 只更新 state 列，避免整行 Save 覆盖其他 goroutine 并发写入的列（如 ai_title）。
func (c *Chats) SaveState(s state.State) error {
	if c == nil {
		return gorm.ErrInvalidDB
	}
	c.SetState(s)
	if c.DB == nil {
		return gorm.ErrInvalidDB
	}
	return c.DB.Model(&Chats{}).Where("id = ?", c.ID).Update("state", s).Error
}

// GetToolState 读取工具执行状态（0 无 / 1 执行中 / 2 已取消）。
func (c *Chats) GetToolState() uint64 {
	if c == nil {
		return 0
	}
	c.runtimeStateMu.RLock()
	defer c.runtimeStateMu.RUnlock()
	return c.ToolState
}

// SetToolState 写入工具执行状态。
func (c *Chats) SetToolState(v uint64) {
	if c == nil {
		return
	}
	c.runtimeStateMu.Lock()
	c.ToolState = v
	c.runtimeStateMu.Unlock()
}

// GetCurrentMessageID 读取当前请求关联的 assistant 消息 DB ID。
func (c *Chats) GetCurrentMessageID() uint64 {
	if c == nil {
		return 0
	}
	c.runtimeStateMu.RLock()
	defer c.runtimeStateMu.RUnlock()
	return c.CurrentMessageID
}

// SetCurrentMessageID 写入当前请求关联的 assistant 消息 DB ID。
func (c *Chats) SetCurrentMessageID(id uint64) {
	if c == nil {
		return
	}
	c.runtimeStateMu.Lock()
	c.CurrentMessageID = id
	c.runtimeStateMu.Unlock()
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

// SetTerminalPushFn registers the callback used to broadcast active terminal snapshots.
func (c *Chats) SetTerminalPushFn(fn func(terminalID, status, content string)) {
	if c == nil {
		return
	}
	c.planPushMu.Lock()
	defer c.planPushMu.Unlock()
	c.TerminalPushFn = fn
}

// PushTerminalUpdate broadcasts a terminal snapshot without holding the callback lock.
func (c *Chats) SetWorkflowEventFn(fn func(runID string, event any)) {
	if c == nil {
		return
	}
	c.planPushMu.Lock()
	defer c.planPushMu.Unlock()
	c.WorkflowEventFn = fn
}

func (c *Chats) SetShellStopFn(fn func(runID, command string, result any)) {
	if c == nil {
		return
	}
	c.planPushMu.Lock()
	defer c.planPushMu.Unlock()
	c.ShellStopFn = fn
}

// SetWorkflowStopFn 注册 workflow 终端结束回调。
func (c *Chats) SetWorkflowStopFn(fn func(runID string, status string, result any)) {
	if c == nil {
		return
	}
	c.planPushMu.Lock()
	defer c.planPushMu.Unlock()
	c.WorkflowStopFn = fn
}

func (c *Chats) PushShellStop(runID, command string, result any) {
	if c == nil {
		return
	}
	c.planPushMu.RLock()
	fn := c.ShellStopFn
	c.planPushMu.RUnlock()
	if fn != nil {
		fn(runID, command, result)
	}
}

// PushWorkflowStop 在 workflow 终端结束时回调（回调未注册时为空操作）。
func (c *Chats) PushWorkflowStop(runID, status string, result any) {
	if c == nil {
		return
	}
	c.planPushMu.RLock()
	fn := c.WorkflowStopFn
	c.planPushMu.RUnlock()
	if fn != nil {
		fn(runID, status, result)
	}
}

func (c *Chats) PushWorkflowEvent(runID string, event any) {
	if c == nil {
		return
	}
	c.planPushMu.RLock()
	fn := c.WorkflowEventFn
	c.planPushMu.RUnlock()
	if fn != nil {
		fn(runID, event)
	}
}

func (c *Chats) PushTerminalUpdate(terminalID, status, content string) {
	if c == nil {
		return
	}
	c.planPushMu.RLock()
	fn := c.TerminalPushFn
	c.planPushMu.RUnlock()
	if fn != nil {
		fn(terminalID, status, content)
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
	if c.ToolCallingContext == nil {
		c.ToolCallingContext = make(map[string]any)
	}
	if c.ToolCallingType == nil {
		c.ToolCallingType = make(map[string]string)
	}
	if c.ToolCallingStreaming == nil {
		c.ToolCallingStreaming = make(map[string]bool)
	}
	if c.ToolCallingRunID == nil {
		c.ToolCallingRunID = make(map[string]string)
	}
	streaming := c.GetState() == state.StateReciving || c.GetState() == state.StateRequesting
	if !streaming {
		// 最终状态（审批后执行 / 工具 PostHook）：用完整原始参数规范化展示内容——
		// 文本块与 calling_info.args 都改成"参数不省略"的渲染；归一化后的参数回写，
		// 直播广播的 content 与 session/resume 回放的完全一致即由此保证。
		if normalized := NormalizeToolCallingParams(c.ToolCallingRawParams[id]); normalized != nil {
			resp = NormalizeToolCallingContent(resp, normalized)
			c.ToolCallingRawParams[id] = normalized
		}
	}
	c.ToolCallingContext[id] = resp
	c.ToolCallingType[id] = typ
	c.ToolCallingStreaming[id] = streaming
	c.toolCtxMu.Unlock()

	// 最终状态（审批后执行/工具 PostHook）的展示内容随消息落库：
	// session/resume 历史回放只能读数据库，此前仅 edit 工具自行持久化，
	// 其余工具的 content（含 calling_info 参数）在还原时全部丢失。
	// 流式增量预览（streaming=true）不落库，避免每 100ms 一次写入。
	if !streaming {
		c.persistToolCallingContent(id, resp)
	}
}

// persistToolCallingContent 把最终状态的工具调用展示内容按工具调用 ID 合并持久化到
// 对应消息行，供 session/resume 回放按 ID 重放 content。工具调用 ID 形如
// call_<chatID>_<msgID>_<toolID>，从中解出消息 ID 与工具 ID；ID 非该格式时回退到
// CurrentMessageID + 整个 ID（仍可回放，只是不与工具结果按 ID 配对）。
func (c *Chats) persistToolCallingContent(id string, content any) {
	if c == nil || c.DB == nil || content == nil || id == "" {
		return
	}
	msgID, toolID := splitToolCallingID(id)
	if msgID == 0 {
		msgID = c.GetCurrentMessageID()
	}
	if toolID == "" {
		toolID = id
	}
	if err := SaveToolCallingContent(c.DB, msgID, toolID, content); err != nil {
		logger.Warn("failed to persist tool calling content for %s: %v", id, err)
	}
}

// splitToolCallingID 解析工具调用 ID（call_<chatID>_<msgID>_<toolID>），
// 返回消息 ID 与工具 ID；格式不符时返回 (0, "")。
func splitToolCallingID(id string) (uint64, string) {
	parts := strings.SplitN(id, "_", 4)
	if len(parts) != 4 || parts[0] != "call" {
		return 0, ""
	}
	msgID, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return 0, ""
	}
	return msgID, parts[3]
}

// SetToolCallingRawParams 记录工具调用的原始参数，供展示 content 规范化使用。
// 由 tools.ExecToolOnHook 在 OnHook 执行前按 session.CurrentToolID 写入。
// 这里只做浅拷贝（流式阶段每次 chunk 都会调用，深拷贝会随参数增长变成 O(n²)）；
// JSON 形态归一化推迟到最终状态与广播时做。
func (c *Chats) SetToolCallingRawParams(id string, raw any) {
	if c == nil || id == "" || raw == nil {
		return
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	if c.ToolCallingRawParams == nil {
		c.ToolCallingRawParams = make(map[string]any)
	}
	c.ToolCallingRawParams[id] = raw
}

// TakeToolCallingRawParams 取出并移除指定工具调用的原始参数（展示 content 规范化用）。
// 未记录时返回 nil；返回值已归一化为 JSON 形态，与回放时从落库 JSON 解出的参数一致。
func (c *Chats) TakeToolCallingRawParams(id string) any {
	if c == nil || id == "" {
		return nil
	}
	c.toolCtxMu.Lock()
	raw, ok := c.ToolCallingRawParams[id]
	if ok {
		delete(c.ToolCallingRawParams, id)
	}
	c.toolCtxMu.Unlock()
	if !ok {
		return nil
	}
	return NormalizeToolCallingParams(raw)
}

// SetToolCallingRunID attaches the run ID to the pending tool callback.
func (c *Chats) SetToolCallingRunID(id, runID string) {
	if c == nil || id == "" || runID == "" {
		return
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	if c.ToolCallingRunID == nil {
		c.ToolCallingRunID = make(map[string]string)
	}
	c.ToolCallingRunID[id] = runID
}

// SetToolCallingTerminalID attaches the terminal ID (unified as @temp/run/<n>) to the pending tool callback.
func (c *Chats) SetToolCallingTerminalID(id, terminalID string) {
	if c == nil || id == "" || terminalID == "" {
		return
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	if c.ToolCallingTerminalID == nil {
		c.ToolCallingTerminalID = make(map[string]string)
	}
	c.ToolCallingTerminalID[id] = terminalID
}

// HasToolCallingFor 判断指定工具调用是否已经有展示内容（工具 OnHook 是否写过）。
// 供 tools.ExecToolOnHook 判断是否需要补一份统一的参数推送。
func (c *Chats) HasToolCallingFor(id string) bool {
	if c == nil || id == "" {
		return false
	}
	c.toolCtxMu.RLock()
	defer c.toolCtxMu.RUnlock()
	_, ok := c.ToolCallingContext[id]
	return ok
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
	c.ToolCallingRawParams = make(map[string]any)
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

// TakeFinalToolCallingWithIDs returns final callbacks together with run IDs and terminal IDs.
func (c *Chats) TakeFinalToolCallingWithIDs() (map[string]any, map[string]string, map[string]string, map[string]string) {
	if c == nil {
		return nil, nil, nil, nil
	}
	c.toolCtxMu.Lock()
	defer c.toolCtxMu.Unlock()
	ctx := make(map[string]any)
	typ := make(map[string]string)
	runIDs := make(map[string]string)
	terminalIDs := make(map[string]string)
	for id := range c.ToolCallingContext {
		if !c.ToolCallingStreaming[id] {
			ctx[id] = c.ToolCallingContext[id]
			typ[id] = c.ToolCallingType[id]
			runIDs[id] = c.ToolCallingRunID[id]
			terminalIDs[id] = c.ToolCallingTerminalID[id]
			delete(c.ToolCallingContext, id)
			delete(c.ToolCallingType, id)
			delete(c.ToolCallingStreaming, id)
			delete(c.ToolCallingRunID, id)
			delete(c.ToolCallingTerminalID, id)
		}
	}
	return ctx, typ, runIDs, terminalIDs
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
	c.ToolCallingRawParams = make(map[string]any)
}

// AppendSystemPrompt adds a transient internal notice to the next model request.
func (c *Chats) AppendSystemPrompt(notice string) {
	if c == nil || notice == "" {
		return
	}
	c.SystemPrompt += notice + "\n"
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
