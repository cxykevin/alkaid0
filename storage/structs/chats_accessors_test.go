package structs

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	confstructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// chatsTestCtxKey 专用 context key 类型，避免与其它包的 key 冲突。
type chatsTestCtxKey struct{}

// openChatsTestDB 打开内存 SQLite 并迁移给定模型（本文件 DB 用例共用）。
func openChatsTestDB(t *testing.T, models ...any) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// readToolCallingContent 读回消息行的 tool_calling_content 列（{"<工具ID>": content}）。
func readToolCallingContent(t *testing.T, db *gorm.DB, messageID uint64) map[string]any {
	t.Helper()
	var msg Messages
	if err := db.First(&msg, messageID).Error; err != nil {
		t.Fatalf("reload message: %v", err)
	}
	if msg.ToolCallingContent == "" {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(msg.ToolCallingContent), &out); err != nil {
		t.Fatalf("unmarshal tool_calling_content %q: %v", msg.ToolCallingContent, err)
	}
	return out
}

// TestChatsEffectiveModelID 模型解析优先级：活跃子代理配了模型时用它，否则回退会话最后选择的模型。
func TestChatsEffectiveModelID(t *testing.T) {
	var nilChats *Chats
	if got := nilChats.EffectiveModelID(); got != 0 {
		t.Errorf("nil receiver = %d, want 0", got)
	}

	c := &Chats{LastModelID: 7}
	if got := c.EffectiveModelID(); got != 7 {
		t.Errorf("无子代理 = %d, want LastModelID 7", got)
	}

	// 只设置 CurrentAgentID、子代理未配模型时必须回退到会话模型。
	c.CurrentAgentID = "agent-1"
	if got := c.EffectiveModelID(); got != 7 {
		t.Errorf("子代理未配模型 = %d, want 回退 7", got)
	}

	c.CurrentAgentConfig = confstructs.AgentConfig{AgentModel: 42}
	if got := c.EffectiveModelID(); got != 42 {
		t.Errorf("子代理模型 = %d, want 42", got)
	}
	if c.LastModelID != 7 {
		t.Errorf("EffectiveModelID 不应改写 LastModelID, got %d", c.LastModelID)
	}
}

// TestChatsAgentLifecycleLock 激活/停用锁必须真正互斥：持锁时 TryLock 失败，解锁后成功。
func TestChatsAgentLifecycleLock(t *testing.T) {
	var nilChats *Chats
	nilChats.AgentLifecycleLock()
	nilChats.AgentLifecycleUnlock()

	c := &Chats{}
	c.AgentLifecycleLock()
	if c.agentLifecycleMu.TryLock() {
		c.agentLifecycleMu.Unlock()
		t.Fatal("AgentLifecycleLock 后仍能再次加锁，锁未生效")
	}
	c.AgentLifecycleUnlock()
	if !c.agentLifecycleMu.TryLock() {
		t.Fatal("AgentLifecycleUnlock 后锁仍未释放")
	}
	c.agentLifecycleMu.Unlock()
}

// TestChatsContextAccessors 未设置、或 holder 存在但 ctx 为空时都要回退 background，绝不返回 nil。
func TestChatsContextAccessors(t *testing.T) {
	c := &Chats{}
	if ctx := c.GetContext(); ctx == nil {
		t.Fatal("GetContext() = nil, want background")
	}
	// holder 已建但 ctx 仍为 nil 的分支。
	c.contextHolder = &contextHolder{}
	if ctx := c.GetContext(); ctx == nil {
		t.Fatal("holder without ctx: GetContext() = nil, want background")
	}

	// 全新会话：SetContext 需要自行创建 holder。
	key := chatsTestCtxKey{}
	fresh := &Chats{}
	fresh.SetContext(context.WithValue(context.Background(), key, "v"))
	if got := fresh.GetContext().Value(key); got != "v" {
		t.Errorf("GetContext() value = %v, want v", got)
	}
	if fresh.contextHolder == nil {
		t.Fatal("SetContext 应创建 contextHolder")
	}
	// 覆盖写入
	fresh.SetContext(context.WithValue(context.Background(), key, "v2"))
	if got := fresh.GetContext().Value(key); got != "v2" {
		t.Errorf("覆盖后 GetContext() value = %v, want v2", got)
	}
}

// TestChatsToolKillFn KillTool 取出并清空注册的中断函数（只中断一次），未注册时是空操作。
func TestChatsToolKillFn(t *testing.T) {
	var nilChats *Chats
	nilChats.SetToolKillFn(func() { t.Error("nil receiver 不应注册中断回调") })
	nilChats.KillTool()

	c := &Chats{}
	c.KillTool() // 未注册：无操作

	calls := 0
	c.SetToolKillFn(func() { calls++ })
	c.KillTool()
	if calls != 1 {
		t.Fatalf("KillTool 调用次数 = %d, want 1", calls)
	}
	c.KillTool()
	if calls != 1 {
		t.Errorf("第二次 KillTool 应无操作, 调用次数 = %d", calls)
	}

	// SetToolKillFn(nil) 是工具执行结束后的清理约定，之后不应再触发。
	c.SetToolKillFn(func() { calls++ })
	c.SetToolKillFn(nil)
	c.KillTool()
	if calls != 1 {
		t.Errorf("SetToolKillFn(nil) 后仍触发了回调, 次数 = %d", calls)
	}
}

// TestChatsPushCallbacks 四组回调（plan / terminal / workflow / shell stop）注册后能收到参数，
// 未注册时静默忽略，重新注册覆盖旧回调。
func TestChatsPushCallbacks(t *testing.T) {
	var nilChats *Chats
	nilChats.SetPlanPushFn(func([]PlanEntry) {})
	nilChats.PushPlan(nil)
	nilChats.SetTerminalPushFn(func(string, string, string) {})
	nilChats.PushTerminalUpdate("t", "s", "c")
	nilChats.SetWorkflowEventFn(func(string, any) {})
	nilChats.PushWorkflowEvent("r", nil)
	nilChats.SetShellStopFn(func(string, string, any) {})
	nilChats.PushShellStop("r", "cmd", nil)

	// 未注册回调时推送不应 panic。
	c := &Chats{}
	c.PushPlan([]PlanEntry{{Content: "x"}})
	c.PushTerminalUpdate("t", "s", "c")
	c.PushWorkflowEvent("r", "e")
	c.PushShellStop("r", "cmd", "res")

	var plan []PlanEntry
	c.SetPlanPushFn(func(entries []PlanEntry) { plan = entries })
	c.PushPlan([]PlanEntry{{Content: "task", Priority: "medium", Status: "pending"}})
	if len(plan) != 1 || plan[0].Content != "task" || plan[0].Priority != "medium" {
		t.Errorf("PushPlan 未把 entries 交给回调: %#v", plan)
	}

	var terminal [3]string
	c.SetTerminalPushFn(func(id, status, content string) { terminal = [3]string{id, status, content} })
	c.PushTerminalUpdate("t-1", "running", "out")
	if terminal != [3]string{"t-1", "running", "out"} {
		t.Errorf("PushTerminalUpdate = %#v", terminal)
	}

	var wfRun string
	var wfEvent any
	c.SetWorkflowEventFn(func(runID string, event any) { wfRun, wfEvent = runID, event })
	c.PushWorkflowEvent("run-1", "evt")
	if wfRun != "run-1" || wfEvent != "evt" {
		t.Errorf("PushWorkflowEvent = (%q, %v)", wfRun, wfEvent)
	}

	var stopArgs [3]any
	c.SetShellStopFn(func(runID, command string, result any) { stopArgs = [3]any{runID, command, result} })
	c.PushShellStop("run-2", "ls", 1)
	if stopArgs != [3]any{"run-2", "ls", 1} {
		t.Errorf("PushShellStop = %#v", stopArgs)
	}

	// 重新注册应覆盖旧回调（旧的不能再被调用）。
	plan = nil
	c.SetPlanPushFn(func([]PlanEntry) { plan = []PlanEntry{{Content: "second"}} })
	c.PushPlan(nil)
	if len(plan) != 1 || plan[0].Content != "second" {
		t.Errorf("重新注册的回调未生效: %#v", plan)
	}
}

// TestChatsNilReceiverAccessors 带 nil 守卫的访问器在 nil receiver 上必须安全返回零值，
// 而不是 panic（会话释放后事件回调仍可能触达这些方法）。
func TestChatsNilReceiverAccessors(t *testing.T) {
	var c *Chats
	if got := c.GetState(); got != state.StateIdle {
		t.Errorf("GetState() = %v, want Idle", got)
	}
	c.SetState(state.StateRequesting)
	c.SetToolState(1)
	c.SetCurrentMessageID(9)
	if got := c.GetToolState(); got != 0 {
		t.Errorf("GetToolState() = %d, want 0", got)
	}
	if got := c.GetCurrentMessageID(); got != 0 {
		t.Errorf("GetCurrentMessageID() = %d, want 0", got)
	}
	if err := c.SaveState(state.StateIdle); err == nil {
		t.Error("SaveState() on nil receiver: err = nil, want gorm.ErrInvalidDB")
	}
	c.SetToolCallingRawParams("call_1", map[string]any{"a": 1})
	if raw := c.TakeToolCallingRawParams("call_1"); raw != nil {
		t.Errorf("TakeToolCallingRawParams() on nil receiver = %v, want nil", raw)
	}
	c.SetToolCallingRunID("call_1", "@temp/run/1")
	c.SetToolCallingTerminalID("call_1", "@temp/run/1")
	c.ClearToolCalling()
	c.ResetLatest()
	c.SetLatest(map[string]any{"a": 1}, map[string]string{"a": "t"})
	if got := c.HasToolCalling(); got {
		t.Error("HasToolCalling() on nil receiver = true, want false")
	}
	if got := c.HasToolCallingFor("call_1"); got {
		t.Error("HasToolCallingFor() on nil receiver = true, want false")
	}
	if ctx, typ := c.TakeFinalToolCalling(); ctx != nil || typ != nil {
		t.Errorf("TakeFinalToolCalling() = (%v, %v), want nils", ctx, typ)
	}
	if ctx, typ, runIDs, terminalIDs := c.TakeFinalToolCallingWithIDs(); ctx != nil || typ != nil || runIDs != nil || terminalIDs != nil {
		t.Error("TakeFinalToolCallingWithIDs() on nil receiver, want all nils")
	}
	if ctx, typ := c.TakeStreamingToolCalling(); ctx != nil || typ != nil {
		t.Errorf("TakeStreamingToolCalling() = (%v, %v), want nils", ctx, typ)
	}
	if ctx, typ := c.SnapshotLatest(); ctx != nil || typ != nil {
		t.Errorf("SnapshotLatest() = (%v, %v), want nils", ctx, typ)
	}
	c.AppendSystemPrompt("notice")

	// 空参数守卫：RunID 的 id / runID 任一为空都不写入。
	c2 := &Chats{}
	c2.SetToolCallingRunID("", "@temp/run/1")
	c2.SetToolCallingRunID("call_1", "")
	if len(c2.ToolCallingRunID) != 0 {
		t.Errorf("空 id/runID 不应写入: %v", c2.ToolCallingRunID)
	}

	// 正常路径：首次写入时自动建 map。
	c3 := &Chats{}
	c3.SetToolCallingRunID("call_1", "@temp/run/9")
	if got := c3.ToolCallingRunID["call_1"]; got != "@temp/run/9" {
		t.Errorf("SetToolCallingRunID = %q, want @temp/run/9", got)
	}
}

// TestChatsSaveStateWithoutDB 无 DB 时 SaveState 报错，但内存状态仍已更新（调用方据此继续会话）。
func TestChatsSaveStateWithoutDB(t *testing.T) {
	c := &Chats{}
	if err := c.SaveState(state.StateWaitApprove); err == nil {
		t.Error("无 DB 的 SaveState(): err = nil, want gorm.ErrInvalidDB")
	}
	if got := c.GetState(); got != state.StateWaitApprove {
		t.Errorf("SaveState 后 GetState() = %v, want WaitApprove", got)
	}
}

// TestChatsTakeFinalAndStreamingToolCalling 最终态与流式增量条目各取各的，互不影响。
func TestChatsTakeFinalAndStreamingToolCalling(t *testing.T) {
	c := &Chats{}
	// 流式阶段（AI 正在接收）写入 → streaming=true
	c.SetState(state.StateReciving)
	c.SetToolCalling("call_stream", "delta", "edit")
	// 最终阶段（审批后执行）写入 → streaming=false
	c.SetState(state.StateIdle)
	c.SetToolCalling("call_final", "done", "edit")

	if !c.HasToolCallingFor("call_stream") || !c.HasToolCallingFor("call_final") {
		t.Fatal("两个工具调用的 HasToolCallingFor 都应为 true")
	}
	if c.HasToolCallingFor("") || c.HasToolCallingFor("call_missing") {
		t.Error("空 id / 未写入的 id 应为 false")
	}

	sctx, styp := c.TakeStreamingToolCalling()
	if len(sctx) != 1 || sctx["call_stream"] != "delta" || styp["call_stream"] != "edit" {
		t.Errorf("TakeStreamingToolCalling = (%v, %v)", sctx, styp)
	}
	if !c.HasToolCallingFor("call_final") {
		t.Error("取流式条目不应影响最终条目")
	}

	fctx, ftyp := c.TakeFinalToolCalling()
	if len(fctx) != 1 || fctx["call_final"] != "done" || ftyp["call_final"] != "edit" {
		t.Errorf("TakeFinalToolCalling = (%v, %v)", fctx, ftyp)
	}
	if c.HasToolCalling() {
		t.Error("最终条目取出后上下文应清空")
	}
}

// TestChatsSetToolCallingNormalizesFinalContent 最终态写入时用完整原始参数重写展示 content
// （文本块 + calling_info.args），流式增量阶段保持原样（否则每个 chunk 都要重渲染）。
func TestChatsSetToolCallingNormalizesFinalContent(t *testing.T) {
	raw := map[string]any{"command": "echo hi"}
	content := []u.H{
		{"type": "content", "content": u.H{"type": "text", "text": "Command: echo …"}},
		{"type": ToolCallingInfoType, "name": "run", "args": u.H{"command": "echo …"}},
	}

	c := &Chats{}
	c.SetToolCallingRawParams("call_1", raw)
	c.SetToolCalling("call_1", content, "run")
	ctx, _, streaming := c.SnapshotToolCalling()
	if streaming["call_1"] {
		t.Error("Idle 阶段写入应标记为最终状态")
	}
	blocks, ok := ctx["call_1"].([]u.H)
	if !ok || len(blocks) != 2 {
		t.Fatalf("content = %#v, want 2 个块", ctx["call_1"])
	}
	if text, _ := blocks[0]["content"].(u.H)["text"].(string); text != "Command: echo hi\n" {
		t.Errorf("文本块 = %q, want 渲染后的完整参数", text)
	}
	if args, ok := blocks[1]["args"].(map[string]any); !ok || args["command"] != "echo hi" {
		t.Errorf("calling_info.args = %#v, want 完整参数", blocks[1]["args"])
	}

	// 流式阶段不规范化。
	c.SetToolCallingRawParams("call_2", raw)
	c.SetState(state.StateReciving)
	c.SetToolCalling("call_2", content, "run")
	ctx2, _, streaming2 := c.SnapshotToolCalling()
	if !streaming2["call_2"] {
		t.Error("Reciving 阶段应标记为流式增量")
	}
	blocks2, ok := ctx2["call_2"].([]u.H)
	if !ok {
		t.Fatalf("content = %#v", ctx2["call_2"])
	}
	if text, _ := blocks2[0]["content"].(u.H)["text"].(string); text != "Command: echo …" {
		t.Errorf("流式阶段 content 不应被规范化, got %q", text)
	}
}

// TestChatsAppendSystemPrompt 追加的内部提示以换行结尾（拼接进系统提示词），空提示不追加。
func TestChatsAppendSystemPrompt(t *testing.T) {
	c := &Chats{SystemPrompt: "base\n"}
	c.AppendSystemPrompt("notice-1")
	c.AppendSystemPrompt("")
	c.AppendSystemPrompt("notice-2")
	want := "base\nnotice-1\nnotice-2\n"
	if c.SystemPrompt != want {
		t.Errorf("SystemPrompt = %q, want %q", c.SystemPrompt, want)
	}
}

// TestChatsPersistToolCallingContent 最终态展示 content 按工具调用 ID 合并落库到消息行，
// 供 session/resume 按 ID 回放；ID 非 call_<chatID>_<msgID>_<toolID> 时回退
// CurrentMessageID + 整个 ID；无法落库时只记日志，不向调用方报错。
func TestChatsPersistToolCallingContent(t *testing.T) {
	db := openChatsTestDB(t, &Chats{}, &Messages{})
	chat := &Chats{}
	if err := db.Create(chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	msg := &Messages{ChatID: chat.ID, Delta: "hi"}
	if err := db.Create(msg).Error; err != nil {
		t.Fatalf("create message: %v", err)
	}

	c := &Chats{ID: chat.ID, DB: db}
	c.SetCurrentMessageID(msg.ID)

	// 无 DB / 空 id / nil content：静默跳过。
	noDB := &Chats{ID: chat.ID, CurrentMessageID: msg.ID}
	noDB.persistToolCallingContent("call_1_1_tool", "x")
	c.persistToolCallingContent("", "x")
	c.persistToolCallingContent("call_1_1_tool", nil)
	if got := readToolCallingContent(t, db, msg.ID); len(got) != 0 {
		t.Errorf("跳过路径不应写入, got %#v", got)
	}

	// 标准 ID：解出消息 ID 与工具 ID，写入 {"tool_a": ...}
	c.persistToolCallingContent(fmt.Sprintf("call_%d_%d_tool_a", chat.ID, msg.ID), []string{"first"})
	got := readToolCallingContent(t, db, msg.ID)
	list, ok := got["tool_a"].([]any)
	if !ok || len(list) != 1 || list[0] != "first" {
		t.Fatalf("标准 ID 落库 = %#v, want tool_a=[first]", got)
	}

	// 非标准 ID：回退 CurrentMessageID + 整个 ID，且与已有条目合并而不是覆盖。
	c.persistToolCallingContent("legacy-id", []string{"second"})
	got = readToolCallingContent(t, db, msg.ID)
	if len(got) != 2 {
		t.Fatalf("合并写入后应有 2 个工具 ID, got %#v", got)
	}
	if _, ok := got["legacy-id"]; !ok {
		t.Errorf("非标准 ID 应以原 ID 落库, got %#v", got)
	}
	if list, _ := got["tool_a"].([]any); len(list) != 1 || list[0] != "first" {
		t.Errorf("合并写入不应覆盖已有条目, got %#v", got)
	}

	// 目标消息不存在：SaveToolCallingContent 报错被记录后吞掉，不影响调用方也不写脏数据。
	c.SetCurrentMessageID(999999)
	c.persistToolCallingContent("legacy-id-2", []string{"third"})
	if _, ok := readToolCallingContent(t, db, msg.ID)["legacy-id-2"]; ok {
		t.Error("目标消息不存在时不应写到其它消息行")
	}
}
