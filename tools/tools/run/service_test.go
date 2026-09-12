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
	if err := Default.Kill(job.Workspace, job.ID); err != nil {
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
	if err := Default.Kill("/tmp", "@temp/run/does-not-exist"); err == nil {
		t.Error("expected error for unknown job ID")
	}
}

func TestServiceStatus(t *testing.T) {
	if job := Default.Status("/tmp", "@temp/run/does-not-exist"); job != nil {
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
	for i := range n {
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
	if err := Default.Kill(job.Workspace, job.ID); err != nil {
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
//
//go:fix inline
func ptrAny(v any) *any { return new(v) }

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
	req.RunID = "@temp/run/test-bg-update"
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
	req.RunID = "@temp/run/test-find"
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
	req.RunID = "@temp/run/test-wait"
	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	runID := "@temp/run/test-wait"
	session := &structs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}
	mp := map[string]*any{
		"type":    new(any("wait")),
		"reason":  new(any("wait for background task")),
		"command": new(any(runID)),
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
		"type":    new(any("wait")),
		"reason":  new(any("wait")),
		"command": new(any("@temp/run/does_not_exist")),
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
		ID: 1,
		DB: db,
		// 终端 ID 的命名空间是工作目录（Root）；激活路径只影响进程工作目录。
		Root:                 "/tmp",
		NowAgent:             "test_agent",
		CurrentActivatePath:  "",
		TemporyDataOfRequest: make(map[string]any),
		TemporyDataOfSession: make(map[string]any),
		TraceID:              0,
	}

	mp := map[string]*any{
		"type":       new(any("shell")),
		"reason":     new(any("test background")),
		"command":    new(any("echo bg-output")),
		"sandbox":    new(any(false)),
		"background": new(any(true)),
		"_id":        new(any("testtool")),
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

// TestServiceListEnded 验证已结束任务枚举：
// 只返回指定会话中已结束的任务，且内容为任务结束时的最终快照
// （与终端全量推送同源：前台与后台任务一致）。
func TestServiceListEnded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	const sessionID uint32 = 4242
	const otherSessionID uint32 = 4243
	ctx := context.Background()

	// 前台任务：结束后也应留下最终内容快照
	foregroundReq := testRunRequest("echo ended-foreground")
	foregroundReq.SessionID = sessionID
	foregroundJob, err := Default.Submit(ctx, foregroundReq)
	if err != nil {
		t.Fatalf("Submit foreground failed: %v", err)
	}
	foregroundJob.Wait(ctx)

	// 后台任务：RunID + UpdateFn（与 run background 一致）
	backgroundReq := testRunRequest("echo ended-background")
	backgroundReq.SessionID = sessionID
	backgroundReq.RunID = "@temp/run/test-list-ended"
	backgroundReq.UpdateFn = func(string) {}
	backgroundJob, err := Default.Submit(ctx, backgroundReq)
	if err != nil {
		t.Fatalf("Submit background failed: %v", err)
	}
	backgroundJob.Wait(ctx)

	// 其它会话的已结束任务不应混入
	otherReq := testRunRequest("echo other-session")
	otherReq.SessionID = otherSessionID
	otherJob, err := Default.Submit(ctx, otherReq)
	if err != nil {
		t.Fatalf("Submit other session failed: %v", err)
	}
	otherJob.Wait(ctx)

	// 运行中的任务不应出现在已结束列表
	runningReq := testRunRequest("sleep 5")
	runningReq.SessionID = sessionID
	runningJob, err := Default.Submit(ctx, runningReq)
	if err != nil {
		t.Fatalf("Submit running failed: %v", err)
	}
	t.Cleanup(func() {
		_ = Default.Kill(runningJob.Workspace, runningJob.ID)
		<-runningJob.Done()
	})

	ended := Default.ListEnded(sessionID)
	byID := make(map[string]*Job, len(ended))
	for _, job := range ended {
		byID[job.ID] = job
	}
	if _, ok := byID[foregroundJob.ID]; !ok {
		t.Errorf("expected finished foreground job %s in ended list", foregroundJob.ID)
	}
	if _, ok := byID[backgroundJob.ID]; !ok {
		t.Errorf("expected finished background job %s in ended list", backgroundJob.ID)
	}
	if _, ok := byID[otherJob.ID]; ok {
		t.Error("ended list should not contain jobs of another session")
	}
	if _, ok := byID[runningJob.ID]; ok {
		t.Error("ended list should not contain a running job")
	}

	// 前台任务的最终内容快照
	fgContent := foregroundJob.Content()
	if !strings.Contains(fgContent, "ended-foreground") {
		t.Errorf("expected foreground final content to include command output, got %q", fgContent)
	}
	if !strings.Contains(fgContent, "Finished: success=true") {
		t.Errorf("expected foreground final content to mark success, got %q", fgContent)
	}
	// 后台任务的最终内容快照
	bgContent := backgroundJob.Content()
	if !strings.Contains(bgContent, "ended-background") {
		t.Errorf("expected background final content to include command output, got %q", bgContent)
	}
	if !strings.Contains(bgContent, "Finished: success=true") {
		t.Errorf("expected background final content to mark success, got %q", bgContent)
	}
}

// TestRunIDHelpers 验证统一 ID（@temp/run/<n>）的分配、校验与内部路径换算。
func TestRunIDHelpers(t *testing.T) {
	const workspace = "/tmp/alkaid0-id-test"

	id := NewRunID(workspace)
	if !strings.HasPrefix(id, RunIDPrefix) {
		t.Fatalf("run id %q should start with %q", id, RunIDPrefix)
	}
	// 同一 workspace 内递增，换 workspace 从 1 重新开始
	if next := NewRunID(workspace); next == id {
		t.Errorf("run id should increase within a workspace: %q", next)
	}
	if first := NewRunID("/tmp/alkaid0-id-test-other"); first != RunIDPrefix+"1" {
		t.Errorf("run id should reset per workspace, got %q", first)
	}

	// 规范形式 → 内部 temp obj 路径
	tempPath, ok := TempPath(id)
	if !ok || tempPath != "run/"+strings.TrimPrefix(id, RunIDPrefix) {
		t.Errorf("TempPath(%q) = %q, %v", id, tempPath, ok)
	}
	// 规范化：规范形式与内部路径都接受，且统一返回规范形式
	if got, ok := NormalizeID(id); !ok || got != id {
		t.Errorf("NormalizeID(%q) = %q, %v", id, got, ok)
	}
	if got, ok := NormalizeID(tempPath); !ok || got != id {
		t.Errorf("NormalizeID(%q) = %q, %v; want %q", tempPath, got, ok, id)
	}
	// 前缀校验：非 @temp/run/<n> 一律拒绝
	for _, bad := range []string{"", "run_1", "@temp/run/", "@temp/run/a/b", "@temp/other/1", "/tmp/run/1", "@temp/run/1/../2"} {
		if got, ok := NormalizeID(bad); ok {
			t.Errorf("NormalizeID(%q) should fail, got %q", bad, got)
		}
		if _, ok := TempPath(bad); ok {
			t.Errorf("TempPath(%q) should fail", bad)
		}
	}
	// 内部路径形式不被 TempPath 接受（它要求规范前缀）
	if _, ok := TempPath(tempPath); ok {
		t.Errorf("TempPath(%q) 要求 %s 前缀，应失败", tempPath, RunIDPrefix)
	}
}

// TestServiceSubmitRunID 验证终端 ID 与 run id 统一为同一个 @temp/run/<n>，
// 且任务以 (工作目录, ID) 索引：ID 在工作目录内唯一（一个工作目录可有多个会话），
// 不同工作目录可以各自有同名 ID 而互不影响。
func TestServiceSubmitRunID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	// 序号按工作目录重置：两个工作目录各自从 1 开始 → 不同目录内允许同名 ID
	runIDA := NewRunID(workspaceA)
	if other := NewRunID(workspaceB); other != runIDA {
		t.Errorf("不同工作目录应各自从 1 开始编号: %q vs %q", other, runIDA)
	}
	// 同一工作目录内的下一个任务拿到下一个序号
	runIDB := NewRunID(workspaceA)

	reqA := testRunRequest("echo run-ids-a")
	reqA.SessionID = 4245
	reqA.Workspace = workspaceA
	reqA.RunID = runIDA
	jobA, err := Default.Submit(context.Background(), reqA)
	if err != nil {
		t.Fatalf("Submit A failed: %v", err)
	}
	jobA.Wait(context.Background())

	reqB := testRunRequest("echo run-ids-b")
	reqB.SessionID = 4246 // 同一工作目录下的另一个会话
	reqB.Workspace = workspaceA
	reqB.RunID = runIDB
	jobB, err := Default.Submit(context.Background(), reqB)
	if err != nil {
		t.Fatalf("Submit B failed: %v", err)
	}
	jobB.Wait(context.Background())

	if jobA.ID != runIDA {
		t.Errorf("job.ID = %q, want %q (终端 ID 与 run id 必须同一个值)", jobA.ID, runIDA)
	}
	if jobA.Workspace != workspaceA {
		t.Errorf("job.Workspace = %q, want %q", jobA.Workspace, workspaceA)
	}
	if got := Default.Status(workspaceA, runIDA); got != jobA {
		t.Error("Status(工作目录, runID) 应命中该 job")
	}
	if got := Default.Status(workspaceB, runIDA); got != nil {
		t.Error("同名 ID 在其它工作目录内不应命中（序号按工作目录重置）")
	}
	// 同一工作目录内不同会话的终端各自可查（ID 在工作目录内唯一，故不冲突）
	if got := Default.Status(workspaceA, runIDB); got != jobB {
		t.Error("同一工作目录内另一个会话的终端应可查到")
	}
	// 按工作目录精确查询（供 wait / kill 使用）
	if got := Default.FindInWorkspace(workspaceA, runIDA); got != jobA {
		t.Error("runid 应登记到 runs（供 wait / kill 使用）")
	}
	if _, ok := TempPath(jobA.ID); !ok {
		t.Errorf("job.ID %q 应为统一的 %s<n> 形式", jobA.ID, RunIDPrefix)
	}
}

// TestServiceSubmitInvalidRunID 验证 Submit 校验 run id 前缀。
func TestServiceSubmitInvalidRunID(t *testing.T) {
	req := testRunRequest("echo bad-id")
	req.RunID = "run_1"
	if _, err := Default.Submit(context.Background(), req); err == nil {
		t.Error("非 @temp/run/<n> 的 run id 应被拒绝")
	}
}

// TestServiceListEndedKilled 验证被终止的任务同样进入已结束列表（状态 killed）。
func TestServiceListEndedKilled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	const sessionID uint32 = 4244
	req := testRunRequest("sleep 5")
	req.SessionID = sessionID
	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if err := Default.Kill(job.Workspace, job.ID); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	<-job.Done()

	ended := Default.ListEnded(sessionID)
	found := false
	for _, item := range ended {
		if item.ID == job.ID {
			found = true
			if item.Status() != JobKilled {
				t.Errorf("expected killed job state, got %v", item.Status())
			}
		}
	}
	if !found {
		t.Errorf("expected killed job %s in ended list", job.ID)
	}
}
