package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// TestRunIDNotResetAfterRestart 回归：进程重启后 run 序号不能重新从 1 开始。
//
// 进程内计数器随重启清零，而 @temp/run/<n> 的内容已持久化（ReferFiles 主键为
// chat_id+path）。修复前新任务复用 run/1，AddTempObject 因主键冲突失败：
// 新任务输出整体丢失，路径上还留着上一进程的旧内容。
func TestRunIDNotResetAfterRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}
	oldDisable := config.GlobalConfig.Agent.DisableSandbox
	config.GlobalConfig.Agent.DisableSandbox = true
	defer func() { config.GlobalConfig.Agent.DisableSandbox = oldDisable }()

	session := runOutputSession(t)
	// 全新工作目录：进程内序号为空，等价于"刚重启"；DB 里的内容则是上一进程留下的
	session.Root = filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(session.Root, 0o755); err != nil {
		t.Fatalf("创建工作目录失败: %v", err)
	}
	const oldContent = "previous-process-content"
	if err := session.DB.Create(&structs.ReferFiles{ChatID: session.ID, Path: "run/1", Content: oldContent}).Error; err != nil {
		t.Fatalf("预置旧临时对象失败: %v", err)
	}

	mp := map[string]*any{
		"type":    new(any("shell")),
		"reason":  new(any("run id persistence")),
		"command": new(any("echo alkaid0-runid-restart")),
	}
	_, _, res, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}

	runID, _ := (*res["path"]).(string)
	if runID == "@temp/run/1" {
		t.Fatalf("重启后复用了已持久化的 run id: %s", runID)
	}
	newPath, ok := TempPath(runID)
	if !ok {
		t.Fatalf("run id 不合法: %q", runID)
	}

	// 旧内容必须原样保留
	var old structs.ReferFiles
	if err := session.DB.Where("chat_id = ? AND path = ?", session.ID, "run/1").First(&old).Error; err != nil {
		t.Fatalf("旧临时对象丢失: %v", err)
	}
	if old.Content != oldContent {
		t.Errorf("旧 run/1 内容被覆盖: %q", old.Content)
	}

	// 新输出必须写进新的 run/<n>
	var fresh structs.ReferFiles
	if err := session.DB.Where("chat_id = ? AND path = ?", session.ID, newPath).First(&fresh).Error; err != nil {
		t.Fatalf("新任务输出未持久化（path=%s）: %v", newPath, err)
	}
	if !strings.Contains(fresh.Content, "alkaid0-runid-restart") {
		t.Errorf("新任务内容缺少命令输出: %q", fresh.Content)
	}
}

// TestBgFinalContentIncludesCreateErr 回归：后台任务创建阶段失败（sandbox.New /
// Execute 失败）没有任何输出，失败原因必须写进最终内容快照。
//
// 修复前内容只有 "[Background] Finished: success=false"：用户/AI 从终端快照
// 完全看不到创建失败的原因（后台任务没有返回错误的通道）。
func TestBgFinalContentIncludesCreateErr(t *testing.T) {
	result := &Result{CreateErr: errors.New("sandbox init failed: unshare denied")}
	content := bgFinalContent("python -c ...", result)
	if !strings.Contains(content, "unshare denied") {
		t.Fatalf("创建失败原因未写入内容快照: %q", content)
	}
}

// TestPanickedJobLeavesActive 回归：execute 的 panic 恢复路径必须把 job 从 active
// 移除。修复前 recover 只写结果并关闭 done，不清理 s.active：任务永久滞留 active，
// 终端列表一直显示 running。
func TestPanickedJobLeavesActive(t *testing.T) {
	const sessionID uint32 = 4249

	req := testRunRequest("echo panic-path")
	req.SessionID = sessionID
	// UpdateFn 在内容快照首帧（execute goroutine 内、runCommand 内部）被调用并 panic，
	// 正好覆盖 runCommand panic 后的恢复路径。
	req.UpdateFn = func(string) { panic("boom from update fn") }

	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("panic 后 job 未结束，可能卡死")
	}
	if job.Status() == JobRunning {
		t.Errorf("panic 后 job 状态仍为 running")
	}
	for _, active := range Default.Active(sessionID) {
		if active == job {
			t.Fatalf("panic 的任务仍滞留在 active（终端列表会一直显示 running）")
		}
	}
}
