package actions

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
	"github.com/cxykevin/alkaid0/ui/funcs"
)

// newWorkflowTestDB 创建带 .alkaid0 的临时工作目录与会话，返回 (cwd, chatID, sessionID)。
func newWorkflowTestDB(t *testing.T) (string, uint32, string) {
	t.Helper()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("create .alkaid0: %v", err)
	}
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	t.Cleanup(func() { closeDB(cwd) })
	chatID, err := funcs.CreateChat(db, false)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	sessionID := registerTestSession(t, cwd, chatID)
	return cwd, chatID, sessionID
}

// TestWorkflowSnapshotNotification 验证 workflow 快照通知的字段形状与"事件尚未落库"的回退。
func TestWorkflowSnapshotNotification(t *testing.T) {
	cwd, chatID, sessionID := newWorkflowTestDB(t)
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	defer closeDB(cwd)

	runID := runTool.NewRunID(cwd)
	now := time.Now().UTC()
	if err := db.Create(&structs.Workflows{
		WorkflowID: runID, ChatID: chatID, RunID: runID, TerminalID: runID,
		Name: "demo", Status: "running", CurrentNode: "n1", CurrentAgent: "a0",
		GraphJSON: "{\"nodes\":[{\"id\":\"n1\"}]}", AgentStateJSON: "{\"n1\":\"running\"}",
		LastSequence: 3, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert workflow failed: %v", err)
	}

	// 有持久化记录：快照带 workflow / graph / agentState
	snap, ok := loadWorkflowSnapshot(sessionID, cwd, chatID, runID, nil)
	if !ok {
		t.Fatal("expected snapshot from persisted record")
	}
	if workflowLastSequence(snap) != 3 {
		t.Errorf("lastSequence = %d, want 3", workflowLastSequence(snap))
	}
	payload := workflowSnapshotNotification(sessionID, runID, snap)
	if payload["sessionUpdate"] != workflowSnapshotUpdate {
		t.Errorf("sessionUpdate = %v, want %v", payload["sessionUpdate"], workflowSnapshotUpdate)
	}
	if payload["runId"] != runID || payload["terminalId"] != runID {
		t.Errorf("runId/terminalId 应等于终端 ID: %v", payload)
	}
	if payload["graph"] == nil || payload["agentState"] == nil || payload["workflow"] == nil {
		t.Errorf("快照应带 workflow / graph / agentState: %v", payload)
	}
	if _, ok := payload["logs"]; ok {
		t.Error("推送快照不应带事件日志（客户端需要时调用 workflow/status）")
	}

	// 事件尚未落库但在内存中是 workflow 终端：至少告知前端"该终端是 workflow"
	otherID := runTool.NewRunID(cwd)
	pending := &runTool.Job{ID: otherID, SessionID: chatID, Workspace: cwd, BackgroundKind: "workflow"}
	pendingSnap, ok := loadWorkflowSnapshot(sessionID, cwd, chatID, otherID, pending)
	if !ok {
		t.Fatal("expected fallback snapshot for active workflow terminal")
	}
	if workflowLastSequence(pendingSnap) != 0 {
		t.Errorf("回退快照 lastSequence 应为 0，实际 %d", workflowLastSequence(pendingSnap))
	}
	if pendingSnap.Workflow == nil {
		t.Error("回退快照也应带 workflow 字段")
	}

	// 既无记录也不是 workflow 终端：不推送
	if _, ok := loadWorkflowSnapshot(sessionID, cwd, chatID, otherID, nil); ok {
		t.Error("非 workflow 终端不应产生快照通知")
	}

	// 去重：同一事件序号只推一次
	sessLock.Lock()
	obj := sessions[sessionID]
	sessLock.Unlock()
	if !markWorkflowSnapshotPushed(obj, runID, 3) {
		t.Error("首次推送应返回 true")
	}
	if markWorkflowSnapshotPushed(obj, runID, 3) {
		t.Error("同一事件序号不应重复推送")
	}
	if !markWorkflowSnapshotPushed(obj, runID, 4) {
		t.Error("事件序号变化后应可再次推送")
	}
}

// TestSessionTerminalListNotifiesWorkflow 验证 terminal/list 返回 workflow 终端时附带快照通知。
func TestSessionTerminalListNotifiesWorkflow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}
	cwd, chatID, sessionID := newWorkflowTestDB(t)
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	defer closeDB(cwd)

	terminalID := runTool.NewRunID(cwd)
	job, err := runTool.Default.Submit(context.Background(), &runTool.Request{
		SessionID: chatID, Workspace: cwd, RunID: terminalID,
		Command: "sleep 10", Shell: "sh", WorkDir: cwd, BackgroundKind: "workflow",
	})
	if err != nil {
		t.Fatalf("submit workflow job failed: %v", err)
	}
	t.Cleanup(func() {
		_ = runTool.Default.Kill(cwd, job.ID)
		<-job.Done()
	})
	now := time.Now().UTC()
	if err := db.Create(&structs.Workflows{
		WorkflowID: terminalID, ChatID: chatID, RunID: terminalID, TerminalID: terminalID,
		Name: "demo", Status: "running", LastSequence: 2, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert workflow failed: %v", err)
	}

	var raw []map[string]any
	call := func(method string, params any, _ *string) error {
		if method == "session/update" {
			if update, ok := params.(SessionUpdate); ok {
				if val, ok := update.Update.(map[string]any); ok {
					raw = append(raw, val)
				}
			}
		}
		return nil
	}

	resp, err := SessionTerminalList(SessionTerminalListRequest{SessionID: sessionID}, call, 1)
	if err != nil {
		t.Fatalf("SessionTerminalList failed: %v", err)
	}
	if len(resp.Terminals) != 1 || !resp.Terminals[0].Workflow {
		t.Fatalf("列表应标记 workflow 终端: %+v", resp.Terminals)
	}
	if len(raw) != 1 || raw[0]["sessionUpdate"] != workflowSnapshotUpdate || raw[0]["terminalId"] != terminalID {
		t.Errorf("列表应附带一条 workflow 快照通知，实际 %v", raw)
	}
}

// TestSessionTerminalStatusNotifiesWorkflow 验证查询 workflow 终端内容时，除 terminal_update
// 之外还会附带一条 workflow 快照通知。
func TestSessionTerminalStatusNotifiesWorkflow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}
	cwd, chatID, sessionID := newWorkflowTestDB(t)
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	defer closeDB(cwd)

	// 该会话的一个 workflow 终端（用普通命令模拟存活的 workflow run）
	terminalID := runTool.NewRunID(cwd)
	job, err := runTool.Default.Submit(context.Background(), &runTool.Request{
		SessionID: chatID, Workspace: cwd, RunID: terminalID,
		Command: "sleep 10", Shell: "sh", WorkDir: cwd, BackgroundKind: "workflow",
	})
	if err != nil {
		t.Fatalf("submit workflow job failed: %v", err)
	}
	t.Cleanup(func() {
		_ = runTool.Default.Kill(cwd, job.ID)
		<-job.Done()
	})
	now := time.Now().UTC()
	if err := db.Create(&structs.Workflows{
		WorkflowID: terminalID, ChatID: chatID, RunID: terminalID, TerminalID: terminalID,
		Name: "demo", Status: "running", LastSequence: 1, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert workflow failed: %v", err)
	}

	var pushed []SessionUpdateUpdate
	var raw []map[string]any
	call := func(method string, params any, _ *string) error {
		if method != "session/update" {
			return nil
		}
		update, ok := params.(SessionUpdate)
		if !ok {
			return nil
		}
		if val, ok := update.Update.(SessionUpdateUpdate); ok {
			pushed = append(pushed, val)
		}
		if val, ok := update.Update.(map[string]any); ok {
			raw = append(raw, val)
		}
		return nil
	}

	resp, err := SessionTerminalStatus(SessionTerminalStatusRequest{SessionID: sessionID, TerminalID: terminalID}, call, 1)
	if err != nil {
		t.Fatalf("SessionTerminalStatus failed: %v", err)
	}
	if !resp.Terminal.Workflow {
		t.Error("workflow 终端应在 TerminalInfo 中标记 workflow")
	}
	if len(pushed) != 1 || pushed[0].SessionUpdate != "alk.cxykevin.top/terminal_update" {
		t.Fatalf("应先推送 terminal_update，实际 %+v", pushed)
	}
	if len(raw) != 1 {
		t.Fatalf("应附带一条 workflow 快照通知，实际 %d 条", len(raw))
	}
	if raw[0]["sessionUpdate"] != workflowSnapshotUpdate || raw[0]["terminalId"] != terminalID {
		t.Errorf("workflow 快照通知字段错误: %v", raw[0])
	}
	if raw[0]["workflow"] == nil {
		t.Errorf("workflow 快照通知应带 workflow 字段: %v", raw[0])
	}
}

// TestSessionTerminalHistoryNotifiesWorkflow 验证回看已结束 workflow 终端时同样附带快照通知，
// 且仅凭持久化记录也能识别 workflow 终端。
func TestSessionTerminalHistoryNotifiesWorkflow(t *testing.T) {
	cwd, chatID, sessionID := newWorkflowTestDB(t)
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	defer closeDB(cwd)

	// 模拟重启前结束的 workflow 终端：只有持久化副本与 workflow 记录，内存中没有 job
	terminalID := runTool.NewRunID(cwd)
	runPath, ok := runTool.TempPath(terminalID)
	if !ok {
		t.Fatalf("invalid run id: %s", terminalID)
	}
	if err := db.Create(&structs.ReferFiles{ChatID: chatID, Path: runPath, Content: "workflow output", ReadOnly: true}).Error; err != nil {
		t.Fatalf("insert refer file failed: %v", err)
	}
	now := time.Now().UTC()
	if err := db.Create(&structs.Workflows{
		WorkflowID: terminalID, ChatID: chatID, RunID: terminalID, TerminalID: terminalID,
		Name: "demo", Status: "completed", LastSequence: 7, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("insert workflow failed: %v", err)
	}

	var raw []map[string]any
	call := func(method string, params any, _ *string) error {
		if method == "session/update" {
			if update, ok := params.(SessionUpdate); ok {
				if val, ok := update.Update.(map[string]any); ok {
					raw = append(raw, val)
				}
			}
		}
		return nil
	}

	resp, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID, TerminalID: terminalID}, call, 1)
	if err != nil {
		t.Fatalf("SessionTerminalHistory failed: %v", err)
	}
	if len(resp.Terminals) != 1 || !resp.Terminals[0].Workflow {
		t.Fatalf("恢复的 workflow 终端应标记 workflow: %+v", resp.Terminals)
	}
	if !resp.Terminals[0].Restored {
		t.Error("该条目应标记 restored")
	}
	found := false
	for _, item := range raw {
		if item["sessionUpdate"] == workflowSnapshotUpdate && item["terminalId"] == terminalID {
			found = true
		}
	}
	if !found {
		t.Errorf("history 应附带 workflow 快照通知，实际 %v", raw)
	}
}
