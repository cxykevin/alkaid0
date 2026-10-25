package actions

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/cronjob"
)

// cronTestConn 测试连接 id（与其它用例的 id 段错开）。
const cronTestConn = uint64(9301)

// cronTimePattern next_runs 的本地时间格式（与 time 工具一致）。
var cronTimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} GMT[+-]\d{1,2}(:\d{2})?$`)

// setupCronRPC 准备临时 cron 库、会话工作目录与连接绑定，返回 (cwd, sessionId, chatID)。
func setupCronRPC(t *testing.T) (string, string, uint32) {
	t.Helper()
	store, err := cronjob.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open cron store: %v", err)
	}
	prevStore := cronjob.Default()
	cronjob.SetDefault(store)
	t.Cleanup(func() {
		cronjob.SetDefault(prevStore)
		_ = store.Close()
	})

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("create .alkaid0: %v", err)
	}
	const chatID uint32 = 7
	sessionID := cwd2SessionID(dir, chatID)

	// 模拟 session/resume 过的连接绑定。
	bindedSessionOnConnMu.Lock()
	if bindedSessionOnConn == nil {
		bindedSessionOnConn = map[uint64][]string{}
	}
	bindedSessionOnConn[cronTestConn] = []string{sessionID}
	bindedSessionOnConnMu.Unlock()
	t.Cleanup(func() {
		bindedSessionOnConnMu.Lock()
		delete(bindedSessionOnConn, cronTestConn)
		bindedSessionOnConnMu.Unlock()
	})
	return dir, sessionID, chatID
}

func addCronJobForTest(t *testing.T, dir string, chatID uint32, expr, prompt string, runOnce bool) cronjob.Job {
	t.Helper()
	job, err := cronjob.Default().Add(cronjob.Job{
		Cron:      expr,
		Workdir:   dir,
		SessionID: chatID,
		Prompt:    prompt,
		RunOnce:   runOnce,
	})
	if err != nil {
		t.Fatalf("add cron job: %v", err)
	}
	return job
}

func TestCronRPCList(t *testing.T) {
	dir, sessionID, chatID := setupCronRPC(t)
	repeating := addCronJobForTest(t, dir, chatID, "0 9 * * *", "daily job", false)
	once := addCronJobForTest(t, dir, chatID, "@weekly", "weekly once", true)
	// 别的会话的任务不应出现在结果里。
	addCronJobForTest(t, dir, chatID+1, "0 0 1 1 *", "other session", true)

	resp, err := CronList(CronListRequest{SessionID: sessionID}, nil, cronTestConn)
	if err != nil {
		t.Fatalf("CronList: %v", err)
	}
	if len(resp.Jobs) != 2 {
		t.Fatalf("jobs = %#v, want 2", resp.Jobs)
	}
	first, second := resp.Jobs[0], resp.Jobs[1]
	if first.ID != repeating.ID || second.ID != once.ID {
		t.Fatalf("job order = %d,%d, want %d,%d", first.ID, second.ID, repeating.ID, once.ID)
	}
	if first.Cron != "0 9 * * *" || first.Prompt != "daily job" || first.RunOnce {
		t.Fatalf("first job = %#v", first)
	}
	if filepath.Clean(first.Workdir) != filepath.Clean(dir) {
		t.Fatalf("workdir = %q, want %q", first.Workdir, dir)
	}
	if len(first.NextRuns) != 4 {
		t.Fatalf("repeating nextRuns = %#v, want 4 entries", first.NextRuns)
	}
	// 简写入库时已展开。
	if second.Cron != "0 0 * * 0" || !second.RunOnce {
		t.Fatalf("second job = %#v", second)
	}
	if len(second.NextRuns) != 1 {
		t.Fatalf("run-once nextRuns = %#v, want 1 entry", second.NextRuns)
	}
	for _, run := range append(first.NextRuns, second.NextRuns...) {
		if !cronTimePattern.MatchString(run) {
			t.Fatalf("nextRuns entry %q, want the time-tool format", run)
		}
	}
}

func TestCronRPCListEmptyAndUnbound(t *testing.T) {
	_, sessionID, chatID := setupCronRPC(t)

	resp, err := CronList(CronListRequest{SessionID: sessionID}, nil, cronTestConn)
	if err != nil {
		t.Fatalf("CronList empty: %v", err)
	}
	if len(resp.Jobs) != 0 || resp.Jobs == nil {
		t.Fatalf("empty list = %#v, want empty non-nil slice", resp.Jobs)
	}

	// 未绑定到本连接 → 拒绝（换一个连接 id 模拟别的客户端）。
	if _, err := CronList(CronListRequest{SessionID: sessionID}, nil, cronTestConn+1); err == nil ||
		!strings.Contains(err.Error(), "not bound to this connection") {
		t.Fatalf("unbound CronList err = %v", err)
	}
	// 空 sessionId。
	if _, err := CronList(CronListRequest{}, nil, cronTestConn); err == nil ||
		!strings.Contains(err.Error(), "sessionId is empty") {
		t.Fatalf("empty sessionId err = %v", err)
	}
	// 会话目录下没有 .alkaid0 时冷解析失败（防止凭字符串在任意目录落库）。
	foreignDir := t.TempDir()
	if _, err := CronList(CronListRequest{SessionID: cwd2SessionID(foreignDir, chatID)}, nil, cronTestConn); err == nil ||
		!strings.Contains(err.Error(), "session not found") {
		t.Fatalf("missing .alkaid0 err = %v", err)
	}
}

func TestCronRPCListAll(t *testing.T) {
	dir, sessionID, chatID := setupCronRPC(t)
	mine := addCronJobForTest(t, dir, chatID, "0 9 * * *", "mine", false)
	otherSession := addCronJobForTest(t, dir, chatID+1, "0 10 * * *", "other session", false)
	otherDir := t.TempDir()
	otherWorkdir := addCronJobForTest(t, otherDir, chatID, "@daily", "other workdir", true)

	resp, err := CronList(CronListRequest{SessionID: sessionID, All: true}, nil, cronTestConn)
	if err != nil {
		t.Fatalf("CronList all: %v", err)
	}
	wantIDs := []uint32{mine.ID, otherSession.ID, otherWorkdir.ID}
	if len(resp.Jobs) != len(wantIDs) {
		t.Fatalf("jobs = %#v, want %d entries", resp.Jobs, len(wantIDs))
	}
	for i, id := range wantIDs {
		if resp.Jobs[i].ID != id {
			t.Fatalf("job[%d].id = %d, want %d", i, resp.Jobs[i].ID, id)
		}
	}
	// 跨会话 / 跨工作目录的任务都能列出，并用 sessionId 标明归属。
	if got, want := resp.Jobs[1].SessionID, cwd2SessionID(dir, chatID+1); got != want {
		t.Fatalf("foreign session id = %q, want %q", got, want)
	}
	if got, want := resp.Jobs[2].SessionID, cwd2SessionID(otherDir, chatID); got != want {
		t.Fatalf("other workdir session id = %q, want %q", got, want)
	}
	if !resp.Jobs[2].RunOnce || resp.Jobs[2].Cron != "0 0 * * *" {
		t.Fatalf("last job = %#v", resp.Jobs[2])
	}

	// all 不影响默认的会话内过滤。
	scoped, err := CronList(CronListRequest{SessionID: sessionID}, nil, cronTestConn)
	if err != nil {
		t.Fatalf("CronList scoped: %v", err)
	}
	if len(scoped.Jobs) != 1 || scoped.Jobs[0].ID != mine.ID {
		t.Fatalf("scoped jobs = %#v, want only %d", scoped.Jobs, mine.ID)
	}
}

func TestCronRPCListAllRequiresBinding(t *testing.T) {
	_, sessionID, _ := setupCronRPC(t)

	// all=true 只是放开结果过滤，sessionId 仍须绑定到本连接。
	if _, err := CronList(CronListRequest{SessionID: sessionID, All: true}, nil, cronTestConn+1); err == nil ||
		!strings.Contains(err.Error(), "not bound to this connection") {
		t.Fatalf("unbound all CronList err = %v", err)
	}
	if _, err := CronList(CronListRequest{All: true}, nil, cronTestConn); err == nil ||
		!strings.Contains(err.Error(), "sessionId is empty") {
		t.Fatalf("empty sessionId all err = %v", err)
	}

	resp, err := CronList(CronListRequest{SessionID: sessionID, All: true}, nil, cronTestConn)
	if err != nil {
		t.Fatalf("CronList all empty: %v", err)
	}
	if len(resp.Jobs) != 0 || resp.Jobs == nil {
		t.Fatalf("empty all list = %#v, want empty non-nil slice", resp.Jobs)
	}
}

func TestCronRPCDelete(t *testing.T) {
	dir, sessionID, chatID := setupCronRPC(t)
	job := addCronJobForTest(t, dir, chatID, "0 9 * * *", "to delete", false)

	resp, err := CronDelete(CronDeleteRequest{SessionID: sessionID, ID: job.ID}, nil, cronTestConn)
	if err != nil {
		t.Fatalf("CronDelete: %v", err)
	}
	if resp.ID != job.ID {
		t.Fatalf("deleted id = %d, want %d", resp.ID, job.ID)
	}
	if _, err := cronjob.Default().Get(job.ID); err == nil {
		t.Fatal("job still exists after delete")
	}

	// 再删一次 → not found。
	if _, err := CronDelete(CronDeleteRequest{SessionID: sessionID, ID: job.ID}, nil, cronTestConn); err == nil ||
		!strings.Contains(err.Error(), "not found in this session") {
		t.Fatalf("second delete err = %v", err)
	}
	// id 为 0。
	if _, err := CronDelete(CronDeleteRequest{SessionID: sessionID}, nil, cronTestConn); err == nil ||
		!strings.Contains(err.Error(), "positive integer") {
		t.Fatalf("zero id err = %v", err)
	}
}

func TestCronRPCDeleteForeignJob(t *testing.T) {
	dir, sessionID, chatID := setupCronRPC(t)
	foreign := addCronJobForTest(t, dir, chatID+1, "0 9 * * *", "other session", false)
	otherDir := t.TempDir()
	otherRoot := addCronJobForTest(t, otherDir, chatID, "0 9 * * *", "other workdir", false)

	for _, id := range []uint32{foreign.ID, otherRoot.ID} {
		if _, err := CronDelete(CronDeleteRequest{SessionID: sessionID, ID: id}, nil, cronTestConn); err == nil ||
			!strings.Contains(err.Error(), "not found in this session") {
			t.Fatalf("delete foreign job %d err = %v", id, err)
		}
	}
	// 别人的任务不能被误删。
	if _, err := cronjob.Default().Get(foreign.ID); err != nil {
		t.Fatalf("foreign job was deleted: %v", err)
	}
	if _, err := cronjob.Default().Get(otherRoot.ID); err != nil {
		t.Fatalf("other-workdir job was deleted: %v", err)
	}
}

func TestCronRPCWithoutStore(t *testing.T) {
	_, sessionID, _ := setupCronRPC(t)
	// 模拟本进程没有启动 cron（Start 失败或未装配）：Default 为空。
	prev := cronjob.Default()
	cronjob.SetDefault(nil)
	t.Cleanup(func() { cronjob.SetDefault(prev) })

	if _, err := CronList(CronListRequest{SessionID: sessionID}, nil, cronTestConn); err == nil ||
		!strings.Contains(err.Error(), "cron is not available") {
		t.Fatalf("CronList without store err = %v", err)
	}
	if _, err := CronDelete(CronDeleteRequest{SessionID: sessionID, ID: 1}, nil, cronTestConn); err == nil ||
		!strings.Contains(err.Error(), "cron is not available") {
		t.Fatalf("CronDelete without store err = %v", err)
	}
}
