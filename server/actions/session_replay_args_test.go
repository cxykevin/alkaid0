package actions

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// replayArgsFixture 隔离的会话库：临时工作目录 + 独立 SQLite + 独立会话注册表。
type replayArgsFixture struct {
	db        *gorm.DB
	chatID    uint32
	sessionID string
	cwd       string
}

// newReplayArgsFixture 搭建回放测试环境（清理顺序在 t.Cleanup 中按 LIFO 保证：
// 先释放会话（等异步索引结束），再关库、关目录，最后恢复全局注册表）。
func newReplayArgsFixture(t *testing.T) *replayArgsFixture {
	t.Helper()
	oldSessions := sessions
	oldDbs := dbs
	sessions = map[string]*sessionObj{}
	dbs = map[string]*dbObj{}
	t.Cleanup(func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		dbLock.Lock()
		dbs = oldDbs
		dbLock.Unlock()
	})
	if config.GlobalConfig == nil {
		config.GlobalConfigSwap(cfgStructs.Config{})
	}

	cwd := t.TempDir()
	t.Cleanup(func() { _ = codebase.CloseDirectory(cwd) })
	db, err := storage.InitStorage(path.Join(cwd, ".alkaid0"), "")
	if err != nil {
		t.Fatalf("InitStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = u.Unwrap(db.DB()).Close() })
	chatID, err := funcs.CreateChat(db)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	sessionID := cwd2SessionID(cwd, chatID)
	t.Cleanup(func() {
		sessLock.Lock()
		obj := sessions[sessionID]
		sessLock.Unlock()
		if obj != nil && obj.indexDone != nil {
			select {
			case <-obj.indexDone:
			case <-time.After(10 * time.Second):
				t.Error("timeout waiting for async index goroutine")
			}
		}
		closeSession(sessionID)
	})
	return &replayArgsFixture{db: db, chatID: chatID, sessionID: sessionID, cwd: cwd}
}

// insertAgentToolCall 落库一条带工具调用的 agent 消息，并返回消息 ID。
func (f *replayArgsFixture) insertAgentToolCall(t *testing.T, calls []map[string]any) uint64 {
	t.Helper()
	raw, err := json.Marshal(calls)
	if err != nil {
		t.Fatalf("marshal tool calling failed: %v", err)
	}
	msg := structs.Messages{ChatID: f.chatID, Type: structs.MessagesRoleAgent, Delta: "calling", ToolCallingJSONString: string(raw)}
	if err := f.db.Create(&msg).Error; err != nil {
		t.Fatalf("insert agent message failed: %v", err)
	}
	for _, call := range calls {
		id, _ := call["id"].(string)
		if err := f.db.Create(&structs.Messages{ChatID: f.chatID, Type: structs.MessagesRoleTool, Delta: replayTestDelta(t, map[string]any{
			"name": call["name"], "id": id, "return": replayTestResult(t, map[string]any{"success": true}),
		})}).Error; err != nil {
			t.Fatalf("insert tool message failed: %v", err)
		}
	}
	return msg.ID
}

// replayToolCallUpdates 执行 session/resume + replayFrom=start，返回回放的 tool_call_update（已按线上 JSON 解码）。
func (f *replayArgsFixture) replayToolCallUpdates(t *testing.T) []map[string]any {
	t.Helper()
	var updates []map[string]any
	call := func(method string, params any, _ *string) error {
		if method != "session/update" {
			return nil
		}
		update, ok := params.(SessionUpdate)
		if !ok {
			return nil
		}
		val, ok := update.Update.(SessionUpdateUpdate)
		if !ok || val.SessionUpdate != "tool_call_update" {
			return nil
		}
		raw, err := json.Marshal(val)
		if err != nil {
			t.Fatalf("marshal update failed: %v", err)
		}
		decoded := map[string]any{}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("unmarshal update failed: %v", err)
		}
		updates = append(updates, decoded)
		return nil
	}
	if _, err := SessionResume(SessionResumeRequest{
		Cwd:        f.cwd,
		SessionID:  f.sessionID,
		ReplayFrom: &ReplayFrom{Type: "start"},
	}, call, 1); err != nil {
		t.Fatalf("SessionResume replay failed: %v", err)
	}
	return updates
}

// callingInfoBlocks 取出回放更新 content 数组里的 alk.cxykevin.top/calling_info 块。
func callingInfoBlocks(t *testing.T, update map[string]any) []map[string]any {
	t.Helper()
	content, ok := update["content"].([]any)
	if !ok {
		return nil
	}
	var out []map[string]any
	for _, block := range content {
		m, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "alk.cxykevin.top/calling_info" {
			out = append(out, m)
		}
	}
	return out
}

// replayTextContent 取出回放更新 content 数组里第一个文本块的内容。
func replayTextContent(t *testing.T, update map[string]any) string {
	t.Helper()
	content, ok := update["content"].([]any)
	if !ok {
		return ""
	}
	for _, block := range content {
		m, ok := block.(map[string]any)
		if !ok || m["type"] != "content" {
			continue
		}
		inner, _ := m["content"].(map[string]any)
		if text, ok := inner["text"].(string); ok {
			return text
		}
	}
	return ""
}

// toolCallUpdateOf 在回放结果里按 toolCallId 找到更新（同一 ID 可能因多条工具结果重复回放）。
func toolCallUpdateOf(updates []map[string]any, toolCallID string) map[string]any {
	for _, update := range updates {
		if id, _ := update["toolCallId"].(string); id == toolCallID {
			return update
		}
	}
	return nil
}

// TestSessionResumeReplayToolCallArgs 验证 session/resume 历史回放为每个工具调用带上参数：
// 私有扩展 content 里的 alk.cxykevin.top/calling_info.args（与直播同形，参数不省略）。
// 覆盖旧数据（tool_calling_content 为空，历史上只有 edit 落库）的兜底重建路径。
func TestSessionResumeReplayToolCallArgs(t *testing.T) {
	fx := newReplayArgsFixture(t)
	msgID := fx.insertAgentToolCall(t, []map[string]any{
		{"name": "run", "id": "tool_run", "parameters": map[string]any{"type": "shell", "command": "echo hi"}},
		{"name": "search", "id": "tool_srch", "parameters": map[string]any{"query": "abc"}},
	})
	updates := fx.replayToolCallUpdates(t)

	cases := []struct {
		toolID     string
		toolName   string
		wantRawKey string
		wantRawVal string
	}{
		{"tool_run", "run", "command", "echo hi"},
		{"tool_srch", "search", "query", "abc"},
	}
	for _, tc := range cases {
		toolCallID := fmt.Sprintf("call_%d_%d_%s", fx.chatID, msgID, tc.toolID)
		update := toolCallUpdateOf(updates, toolCallID)
		if update == nil {
			t.Fatalf("回放缺少工具调用 %s，实际 %v", toolCallID, updates)
		}
		// 协议不再发送 rawInput：参数只走 content（文本块 + calling_info.args）
		if _, ok := update["rawInput"]; ok {
			t.Errorf("%s 回放不应携带 rawInput（参数经 content 给出）: %v", tc.toolID, update)
		}
		// 私有扩展：calling_info 必然存在且带 args
		infos := callingInfoBlocks(t, update)
		if len(infos) == 0 {
			t.Fatalf("%s 回放缺少 alk.cxykevin.top/calling_info: %v", tc.toolID, update)
		}
		if name, _ := infos[0]["name"].(string); name != tc.toolName {
			t.Errorf("%s calling_info.name = %q, want %q", tc.toolID, name, tc.toolName)
		}
		args, ok := infos[0]["args"].(map[string]any)
		if !ok {
			t.Fatalf("%s calling_info.args 不是对象: %v", tc.toolID, infos[0])
		}
		if got, _ := args[tc.wantRawKey].(string); got != tc.wantRawVal {
			t.Errorf("%s calling_info.args[%s] = %q, want %q", tc.toolID, tc.wantRawKey, got, tc.wantRawVal)
		}
		if text := replayTextContent(t, update); !strings.Contains(text, tc.wantRawVal) {
			t.Errorf("%s 回放文本预览应包含参数值 %q，实际 %q", tc.toolID, tc.wantRawVal, text)
		}
	}
}

// TestSetToolCallingPersistsContentForReplay 验证最终状态的工具调用展示内容会随消息落库，
// 且回放优先使用落库内容（不再依赖 edit 工具自行持久化）；流式增量不落库。
func TestSetToolCallingPersistsContentForReplay(t *testing.T) {
	fx := newReplayArgsFixture(t)
	msgID := fx.insertAgentToolCall(t, []map[string]any{
		{"name": "run", "id": "tool_run", "parameters": map[string]any{"type": "shell", "command": "echo hi"}},
	})

	sess, err := funcs.QueryChat(fx.db, fx.chatID)
	if err != nil {
		t.Fatalf("QueryChat failed: %v", err)
	}
	sess.DB = fx.db
	sess.CurrentMessageID = msgID
	// StateToolCalling 之外的“最终”阶段：模拟审批后执行时 OnHook 写入
	sess.State = state.StateToolCalling
	finalContent := []u.H{
		{"type": "content", "content": u.H{"type": "text", "text": "PERSISTED-MARKER"}},
		{"type": "alk.cxykevin.top/calling_info", "name": "run", "messageID": msgID, "args": u.H{"command": "echo hi"}},
	}
	toolCallID := fmt.Sprintf("call_%d_%d_%s", fx.chatID, msgID, "tool_run")
	sess.SetToolCalling(toolCallID, finalContent, "run")

	var stored structs.Messages
	if err := fx.db.First(&stored, msgID).Error; err != nil {
		t.Fatalf("reload message failed: %v", err)
	}
	if !strings.Contains(stored.ToolCallingContent, "PERSISTED-MARKER") || !strings.Contains(stored.ToolCallingContent, "tool_run") {
		t.Fatalf("最终工具调用展示内容应落库，实际 %q", stored.ToolCallingContent)
	}

	// 流式增量预览（StateReciving）不落库
	sess.State = state.StateReciving
	sess.SetToolCalling(fmt.Sprintf("call_%d_%d_%s", fx.chatID, msgID, "tool_stream"), []u.H{{"type": "content", "content": u.H{"type": "text", "text": "STREAM-MARKER"}}}, "run")
	var afterStream structs.Messages
	if err := fx.db.First(&afterStream, msgID).Error; err != nil {
		t.Fatalf("reload message failed: %v", err)
	}
	if strings.Contains(afterStream.ToolCallingContent, "STREAM-MARKER") {
		t.Errorf("流式增量预览不应落库，实际 %q", afterStream.ToolCallingContent)
	}

	// 回放应使用落库内容（而非按参数重建的兜底内容）
	updates := fx.replayToolCallUpdates(t)
	update := toolCallUpdateOf(updates, toolCallID)
	if update == nil {
		t.Fatalf("回放缺少工具调用 %s，实际 %v", toolCallID, updates)
	}
	if text := replayTextContent(t, update); text != "PERSISTED-MARKER" {
		t.Errorf("回放应使用落库的展示内容，实际文本 %q", text)
	}
	if _, ok := update["rawInput"]; ok {
		t.Errorf("回放不应携带 rawInput: %v", update)
	}
}

// TestLiveAndResumeToolCallingContentIdentical 验证同一个工具调用在直播与
// session/resume 回放两条链路上产出的 content **逐字节一致**，且参数不省略：
// 工具自建的展示文本/args 是裁剪子集（run 会漏掉 timeout/background），
// 规范化后两端都必须带上全部原始参数。
func TestLiveAndResumeToolCallingContentIdentical(t *testing.T) {
	fx := newReplayArgsFixture(t)
	msgID := fx.insertAgentToolCall(t, []map[string]any{
		{"name": "run", "id": "tool_run", "parameters": map[string]any{
			"type": "shell", "reason": "long loop", "command": "seq 1 3",
			"timeout": float64(30), "background": true,
		}},
	})

	sess, err := funcs.QueryChat(fx.db, fx.chatID)
	if err != nil {
		t.Fatalf("QueryChat failed: %v", err)
	}
	sess.DB = fx.db
	sess.CurrentMessageID = msgID
	sess.State = state.StateToolCalling

	toolCallID := fmt.Sprintf("call_%d_%d_%s", fx.chatID, msgID, "tool_run")
	// 原始参数（ExecToolOnHook 记录的形态：map[string]*any）
	timeout := any(float64(30))
	background := any(true)
	sess.SetToolCallingRawParams(toolCallID, map[string]*any{
		"type":       anyPtrOf("shell"),
		"reason":     anyPtrOf("long loop"),
		"command":    anyPtrOf("seq 1 3"),
		"timeout":    &timeout,
		"background": &background,
	})
	// 工具自建的裁剪版展示内容（run 的现状：文本与 args 都没有 timeout/background）
	curated := []u.H{
		{"type": "content", "content": u.H{"type": "text", "text": "Type: shell\nReason: long loop\nCommand: seq 1 3\nSandbox: false\n"}},
		{"type": structs.ToolCallingInfoType, "name": "run", "messageID": msgID,
			"args": u.H{"type": "shell", "reason": "long loop", "command": "seq 1 3", "sandbox": nil}},
	}
	sess.SetToolCalling(toolCallID, curated, "run")

	// 直播侧：广播出去的展示内容（规范化后；rawInput 已按协议要求不再发送）。
	liveCtx, _, _ := sess.SnapshotToolCalling()
	liveContent, ok := liveCtx[toolCallID]
	if !ok {
		t.Fatal("直播侧应写入 ToolCallingContext")
	}
	liveJSON, err := json.Marshal(liveContent)
	if err != nil {
		t.Fatalf("marshal live content failed: %v", err)
	}

	// 回放侧
	update := toolCallUpdateOf(fx.replayToolCallUpdates(t), toolCallID)
	if update == nil {
		t.Fatalf("回放缺少工具调用 %s", toolCallID)
	}
	replayJSON, err := json.Marshal(update["content"])
	if err != nil {
		t.Fatalf("marshal replay content failed: %v", err)
	}

	if string(liveJSON) != string(replayJSON) {
		t.Errorf("直播与回放的 content 必须完全一致:\n  live  = %s\n  replay= %s", liveJSON, replayJSON)
	}

	// 参数不省略：工具自建文本/args 漏掉的 timeout/background 必须在两端可见
	text := replayTextContent(t, update)
	for _, want := range []string{"Timeout: 30", "Background: true", "Command: seq 1 3"} {
		if !strings.Contains(text, want) {
			t.Errorf("展示文本应包含 %q（参数不省略），实际 %q", want, text)
		}
	}
	infos := callingInfoBlocks(t, update)
	if len(infos) == 0 {
		t.Fatalf("回放缺少 calling_info: %v", update)
	}
	args, _ := infos[0]["args"].(map[string]any)
	if _, ok := args["timeout"]; !ok {
		t.Errorf("calling_info.args 应包含完整参数（timeout），实际 %v", args)
	}
	if _, ok := args["background"]; !ok {
		t.Errorf("calling_info.args 应包含完整参数（background），实际 %v", args)
	}
}

// anyPtrOf 返回指向给定字符串的 any 指针（模拟 parser 给出的参数表）。
func anyPtrOf(s string) *any {
	v := any(s)
	return &v
}
