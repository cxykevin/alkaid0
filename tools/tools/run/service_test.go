package run

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// testRunRequest 构造一个最小化的命令执行请求（无沙盒，避免依赖 unshare）。
func testRunRequest(command string) *Request {
	return &Request{
		Command: command,
		Shell:   getShell(""),
		WorkDir: os.TempDir(),
		Sandbox: false,
	}
}

func TestServiceSubmitEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	ctx := context.Background()
	job, err := Default.Submit(ctx, testRunRequest("echo hello-background"))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if job == nil || job.ID == "" {
		t.Fatal("expected non-empty job ID")
	}

	result := job.Wait(ctx)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
	if !strings.Contains(result.Output, "hello-background") {
		t.Errorf("expected output to contain command output, got %q", result.Output)
	}
	if job.Status() != JobFinished {
		t.Errorf("expected job finished, got %v", job.Status())
	}
}

func TestServiceKillByContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	ctx, cancel := context.WithCancel(context.Background())
	job, err := Default.Submit(ctx, testRunRequest("sleep 30"))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// 命令启动后取消 context，验证不 hang 且被终止
	time.Sleep(150 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		job.Wait(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("job.Wait did not return after context cancel, possibly hang")
	}

	if job.Status() != JobKilled {
		t.Errorf("expected job killed, got %v", job.Status())
	}
}

func TestServiceKillByID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	job, err := Default.Submit(context.Background(), testRunRequest("sleep 30"))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if err := Default.Kill(job.ID); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		job.Wait(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("job.Wait did not return after Kill, possibly hang")
	}

	if job.Status() != JobKilled {
		t.Errorf("expected job killed, got %v", job.Status())
	}
}

func TestServiceKillUnknownID(t *testing.T) {
	if err := Default.Kill("run_does_not_exist"); err == nil {
		t.Error("expected error for unknown job ID")
	}
}

func TestServiceStatus(t *testing.T) {
	if job := Default.Status("run_does_not_exist"); job != nil {
		t.Errorf("expected nil for unknown job, got %+v", job)
	}
}

// TestServiceConcurrent 验证多个命令可并发执行且互不阻塞（后台服务不串行执行命令）。
func TestServiceConcurrent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	const n = 5
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := "sleep 0.2 && echo job" + string(rune('0'+idx))
			job, err := Default.Submit(context.Background(), testRunRequest(cmd))
			if err != nil {
				t.Errorf("Submit(%d) failed: %v", idx, err)
				return
			}
			result := job.Wait(context.Background())
			if result == nil || !result.Success {
				t.Errorf("job %d not successful: %+v", idx, result)
			}
		}(i)
	}
	wg.Wait()
}

// TestServiceKillBeforeStart 验证在命令真正启动前 kill（killFn 尚未注册）不会漏杀。
func TestServiceKillBeforeStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	job, err := Default.Submit(context.Background(), testRunRequest("sleep 30"))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	// 立即 kill（execute goroutine 可能尚未注册 killFn）
	if err := Default.Kill(job.ID); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		job.Wait(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("job.Wait did not return after early kill, possibly hang")
	}

	if job.Status() != JobKilled {
		t.Errorf("expected job killed, got %v", job.Status())
	}
}

// ptrAny 构造 *any 参数，用于 runTask 的 mp。
func ptrAny(v any) *any { return &v }

// setupTestDB 构造内存 SQLite 测试库。
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(&structs.Traces{}, &structs.Chats{}, &structs.ReferFiles{}); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
	return db
}

// TestServiceBackgroundUpdateFn 验证后台任务的 UpdateFn：
// 命令运行期间按 backgroundUpdateInterval 定时刷新（Running...），结束后写最终结果。
func TestServiceBackgroundUpdateFn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	old := backgroundUpdateInterval
	backgroundUpdateInterval = 30 * time.Millisecond
	defer func() { backgroundUpdateInterval = old }()

	var mu sync.Mutex
	runningCount := 0
	finalContent := ""
	updateFn := func(content string) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(content, "Running...") {
			runningCount++
		} else {
			finalContent = content
		}
	}

	req := testRunRequest("sleep 0.2 && echo bg-update-done")
	req.RunID = "run/test-bg-update"
	req.UpdateFn = updateFn

	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("background job did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if runningCount == 0 {
		t.Error("expected ticker to fire at least once (Running... update)")
	}
	if !strings.Contains(finalContent, "Finished: success=true") {
		t.Errorf("expected final content to mark success, got %q", finalContent)
	}
	if !strings.Contains(finalContent, "bg-update-done") {
		t.Errorf("expected final content to include command output, got %q", finalContent)
	}
}

// TestServiceFind 验证 runid → job 映射（含 @temp/ 前缀归一化）。
func TestServiceFind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	req := testRunRequest("echo find-me")
	req.RunID = "run/test-find"
	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	defer func() { <-job.Done() }()

	if got := Default.Find("run/test-find"); got != job {
		t.Error("Find with internal path did not match")
	}
	if got := Default.Find("@temp/run/test-find"); got != job {
		t.Error("Find with @temp/ prefix did not match")
	}
	if got := Default.Find("run/nonexistent"); got != nil {
		t.Errorf("expected nil for unknown runid, got %+v", got)
	}
}

// TestWaitTask 验证 wait 类型阻塞直到后台任务结束。
func TestWaitTask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	req := testRunRequest("sleep 0.2 && echo wait-done")
	req.RunID = "run/test-wait"
	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	runID := "@temp/run/test-wait"
	session := &structs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}
	mp := map[string]*any{
		"type":    ptrAny("wait"),
		"reason":  ptrAny("wait for background task"),
		"command": ptrAny(runID),
	}

	done := make(chan struct{})
	var pass bool
	var res map[string]*any
	var werr error
	go func() {
		pass, _, res, werr = waitTask(session, mp, []*any{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitTask did not return after background task finished, possibly hang")
	}

	if werr != nil {
		t.Fatalf("waitTask returned error: %v", werr)
	}
	if pass {
		t.Error("expected pass to be false")
	}
	if success, ok := (*(res["success"])).(bool); !ok || !success {
		t.Errorf("expected success to be true, got %v", res["success"])
	}
	if path, ok := (*(res["path"])).(string); !ok || path != runID {
		t.Errorf("expected path to be %q, got %v", runID, res["path"])
	}

	select {
	case <-job.Done():
	default:
		t.Error("background job should have finished before wait returned")
	}
}

// TestWaitTaskUnknownRunID 验证 wait 传入不存在的 runid 返回错误。
func TestWaitTaskUnknownRunID(t *testing.T) {
	session := &structs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}
	mp := map[string]*any{
		"type":    ptrAny("wait"),
		"reason":  ptrAny("wait"),
		"command": ptrAny("@temp/run/does_not_exist"),
	}

	pass, _, res, err := waitTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("expected pass to be false")
	}
	if success, ok := (*(res["success"])).(bool); !ok || success {
		t.Errorf("expected success to be false, got %v", res["success"])
	}
}

// TestRunTaskBackground 端到端验证 background=true：立即返回 runid、
// 创建初始 temp obj、命令结束后 temp obj 更新为最终结果。
func TestRunTaskBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	db := setupTestDB(t)
	if err := db.Create(&structs.Chats{ID: 1, TraceID: 0}).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}
	session := &structs.Chats{
		ID:                   1,
		DB:                   db,
		NowAgent:             "test_agent",
		CurrentActivatePath:  "/tmp",
		TemporyDataOfRequest: make(map[string]any),
		TemporyDataOfSession: make(map[string]any),
		TraceID:              0,
	}

	mp := map[string]*any{
		"type":       ptrAny("shell"),
		"reason":     ptrAny("test background"),
		"command":    ptrAny("echo bg-output"),
		"sandbox":    ptrAny(false),
		"background": ptrAny(true),
		"_id":        ptrAny("testtool"),
	}

	start := time.Now()
	pass, _, res, err := runTask(session, mp, []*any{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("runTask returned error: %v", err)
	}
	if pass {
		t.Error("expected pass to be false")
	}
	if elapsed > 2*time.Second {
		t.Errorf("background runTask should return immediately, took %v", elapsed)
	}

	runID, ok := (*(res["run_id"])).(string)
	if !ok || !strings.HasPrefix(runID, "@temp/run/") {
		t.Fatalf("expected run_id to be @temp/run/..., got %v", res["run_id"])
	}
	if bg, ok := (*(res["background"])).(bool); !ok || !bg {
		t.Errorf("expected background to be true, got %v", res["background"])
	}
	if success, ok := (*(res["success"])).(bool); !ok || !success {
		t.Errorf("expected success to be true, got %v", res["success"])
	}

	internalPath := strings.TrimPrefix(runID, "@temp/")

	// 初始 temp obj 应立即存在
	var rf structs.ReferFiles
	if err := db.Where("chat_id = ? AND path = ?", session.ID, internalPath).First(&rf).Error; err != nil {
		t.Fatalf("initial temp object not found: %v", err)
	}
	if !strings.Contains(rf.Content, "Submitted") {
		t.Errorf("expected initial content to contain Submitted, got %q", rf.Content)
	}

	// 等待命令结束后 temp obj 更新为最终结果
	job := Default.Find(runID)
	if job == nil {
		t.Fatal("runid not registered in service")
	}
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("background job did not finish")
	}

	if err := db.Where("chat_id = ? AND path = ?", session.ID, internalPath).First(&rf).Error; err != nil {
		t.Fatalf("temp object not found after finish: %v", err)
	}
	if !strings.Contains(rf.Content, "Finished: success=true") {
		t.Errorf("expected final content to mark success, got %q", rf.Content)
	}
	if !strings.Contains(rf.Content, "bg-output") {
		t.Errorf("expected final content to include output, got %q", rf.Content)
	}
}
