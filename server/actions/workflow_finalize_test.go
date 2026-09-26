package actions

import (
	"context"
	"runtime"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
)

// TestApplyWorkflowFinalize 验证终态实时落库：首个事件建行写 running + started_at，
// 结束时写 status/finished_at/error/result_path。
func TestApplyWorkflowFinalize(t *testing.T) {
	cwd, chatID, sessionID := newWorkflowTestDB(t)
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	defer closeDB(cwd)

	runID := runTool.NewRunID(cwd)
	persistWorkflowEvent(sessionID, runID, runTool.WorkflowEvent{
		Type: "graph",
		Raw:  []byte(`{"type":"graph"}`),
		Data: map[string]any{"type": "graph"},
	})
	var row structs.Workflows
	if err := db.Where("chat_id = ? AND run_id IN ?", chatID, workflowStoredRunIDs(chatID, runID)).First(&row).Error; err != nil {
		t.Fatalf("workflow row not created: %v", err)
	}
	if row.Status != "running" || row.StartedAt == nil {
		t.Fatalf("首个事件应写 running + started_at: %+v", row)
	}

	applyWorkflowFinalize(sessionID, runID, &workflowFinalize{
		status: runTool.JobKilled.String(),
		result: &runTool.Result{Success: false, ErrString: "killed by user"},
	})
	if err := db.Where("id = ?", row.ID).First(&row).Error; err != nil {
		t.Fatalf("reload workflow failed: %v", err)
	}
	if row.Status != "killed" {
		t.Errorf("status = %q, want killed", row.Status)
	}
	if row.FinishedAt == nil {
		t.Error("finished_at 应写入")
	}
	if row.Error != "killed by user" {
		t.Errorf("error = %q", row.Error)
	}
	if row.ResultPath != runID {
		t.Errorf("result_path = %q, want %q", row.ResultPath, runID)
	}
}

// TestSessionWorkflowControlRequiresActiveRun 验证 input/stop 只允许活动 workflow：
// 已结束的 workflow 终端即使仍在内存中也不能再控制。
func TestSessionWorkflowControlRequiresActiveRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}
	cwd, chatID, sessionID := newWorkflowTestDB(t)
	runID := runTool.NewRunID(cwd)
	job, err := runTool.Default.Submit(context.Background(), &runTool.Request{
		SessionID: chatID, Workspace: cwd, RunID: runID,
		Command: "true", Shell: "sh", WorkDir: cwd, BackgroundKind: "workflow",
	})
	if err != nil {
		t.Fatalf("submit workflow job failed: %v", err)
	}
	<-job.Done()

	if _, err := SessionWorkflowInput(SessionWorkflowInputRequest{
		SessionID: sessionID, RunID: runID, Input: map[string]any{"cmd": "shutdown"},
	}, nil, 1); err == nil {
		t.Fatal("已结束的 workflow 不应接受 input")
	}
	if _, err := SessionWorkflowStop(SessionWorkflowRequest{SessionID: sessionID, RunID: runID}, nil, 1); err == nil {
		t.Fatal("已结束的 workflow 不应接受 stop")
	}
}
