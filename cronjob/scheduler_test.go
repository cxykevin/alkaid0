package cronjob

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

// dispatchCall 记录一次投递。
type dispatchCall struct {
	workdir   string
	sessionID uint32
	prompt    string
}

// fakeBackend 调度器测试替身。
type fakeBackend struct {
	mu         sync.Mutex
	missing    map[uint32]bool
	dirMissing bool
	busy       bool
	err        error
	calls      []dispatchCall
	notify     chan dispatchCall
	// attempt 每次 Dispatch 进入时（无论成功失败）都会收到一个信号。
	attempt chan struct{}
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		missing: map[uint32]bool{},
		notify:  make(chan dispatchCall, 16),
		attempt: make(chan struct{}, 16),
	}
}

func (f *fakeBackend) SessionExists(workdir string, sessionID uint32) (bool, error) {
	if f.dirMissing {
		return false, ErrWorkdirMissing
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.missing[sessionID], nil
}

func (f *fakeBackend) Dispatch(workdir string, sessionID uint32, prompt string) error {
	select {
	case f.attempt <- struct{}{}:
	default:
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dirMissing {
		return ErrWorkdirMissing
	}
	if f.missing[sessionID] {
		return ErrSessionMissing
	}
	if f.err != nil {
		return f.err
	}
	if f.busy {
		return ErrBusy
	}
	call := dispatchCall{workdir: workdir, sessionID: sessionID, prompt: prompt}
	f.calls = append(f.calls, call)
	select {
	case f.notify <- call:
	default:
	}
	return nil
}

func (f *fakeBackend) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testScheduler(t *testing.T, backend Backend) (*Scheduler, *Store) {
	t.Helper()
	s := testStore(t)
	return newScheduler(s, backend), s
}

func waitCall(t *testing.T, backend *fakeBackend) dispatchCall {
	t.Helper()
	select {
	case c := <-backend.notify:
		return c
	case <-time.After(2 * time.Second):
		t.Fatalf("dispatch was not called")
		return dispatchCall{}
	}
}

func TestSchedulerFiresDueJobOnceAndRecomputesNext(t *testing.T) {
	backend := newFakeBackend()
	s, store := testScheduler(t, backend)
	// 工作目录必须真实存在，否则 cleanup 会把它删掉（这里只测 tick，不跑 cleanup）。
	dir := t.TempDir()
	job, err := store.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 3, Prompt: "ping"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.reload()

	s.mu.Lock()
	next := s.entries[job.ID].next
	s.mu.Unlock()

	s.tick(next)
	call := waitCall(t, backend)
	if call.prompt != "ping" || call.sessionID != 3 || call.workdir != filepath.Clean(dir) {
		t.Fatalf("dispatch = %#v, want prompt/session/workdir", call)
	}

	// 同一次 tick 时刻重复触发不应再次投递（下一次触发时间已推进）。
	s.tick(next)
	select {
	case c := <-backend.notify:
		t.Fatalf("job fired twice: %#v", c)
	case <-time.After(200 * time.Millisecond):
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("dispatch count = %d, want 1", got)
	}
}

func TestSchedulerDoesNotCatchUp(t *testing.T) {
	backend := newFakeBackend()
	s, store := testScheduler(t, backend)
	dir := t.TempDir()
	if _, err := store.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 1, Prompt: "tick"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.reload()

	// 模拟服务停机一天后恢复：只触发一次，不补跑错过的 1440 次。
	s.tick(time.Now().Add(24 * time.Hour))
	waitCall(t, backend)
	select {
	case c := <-backend.notify:
		t.Fatalf("unexpected extra fire without catch-up: %#v", c)
	case <-time.After(200 * time.Millisecond):
	}
	if got := backend.callCount(); got != 1 {
		t.Fatalf("dispatch count = %d, want 1 (no catch-up)", got)
	}
}

func TestSchedulerSkipsBusySessionAndKeepsJob(t *testing.T) {
	backend := newFakeBackend()
	backend.busy = true
	s, store := testScheduler(t, backend)
	dir := t.TempDir()
	job, err := store.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 5, Prompt: "busy"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.reload()

	s.mu.Lock()
	next := s.entries[job.ID].next
	s.mu.Unlock()
	s.tick(next)

	// 等 fire goroutine 结束（busy 不会写 notify，用轮询 in-flight 状态代替）。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		running := s.inflight[job.ID]
		s.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// fakeBackend 在 busy 时直接返回 ErrBusy，不记录 calls；这里期望 0。
	if got := backend.callCount(); got != 0 {
		t.Fatalf("dispatch count = %d, want 0", got)
	}
	if _, err := store.Get(job.ID); err != nil {
		t.Fatalf("busy job should be kept, got %v", err)
	}
}

func TestSchedulerRemovesJobWhenSessionMissing(t *testing.T) {
	backend := newFakeBackend()
	backend.missing[9] = true
	s, store := testScheduler(t, backend)
	dir := t.TempDir()
	job, err := store.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 9, Prompt: "gone"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.reload()

	s.mu.Lock()
	next := s.entries[job.ID].next
	s.mu.Unlock()
	s.tick(next)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.Get(job.ID); errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %d should be deleted when its session is missing", job.ID)
}

func TestSchedulerRemovesJobWhenWorkdirMissing(t *testing.T) {
	backend := newFakeBackend()
	backend.dirMissing = true
	s, store := testScheduler(t, backend)
	dir := t.TempDir()
	job, err := store.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 1, Prompt: "gone"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.reload()

	s.mu.Lock()
	next := s.entries[job.ID].next
	s.mu.Unlock()
	s.tick(next)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.Get(job.ID); errors.Is(err, gorm.ErrRecordNotFound) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %d should be deleted when its workdir is missing", job.ID)
}

func TestCleanupRemovesInvalidJobs(t *testing.T) {
	backend := newFakeBackend()
	backend.missing[4] = true
	s, store := testScheduler(t, backend)
	dir := t.TempDir()

	valid, err := store.Add(Job{Cron: "0 * * * *", Workdir: dir, SessionID: 1, Prompt: "ok"})
	if err != nil {
		t.Fatalf("Add valid: %v", err)
	}
	// 绕过 Validate 直接写入各种失效行（模拟阶段二工具或历史数据）。
	rows := []Job{
		{Cron: "not a cron", Workdir: dir, SessionID: 1, Prompt: "bad cron"},
		{Cron: "* * * * *", Workdir: dir, SessionID: 1, Prompt: "   "},
		{Cron: "* * * * *", Workdir: filepath.Join(dir, "no-such-dir"), SessionID: 1, Prompt: "bad dir"},
		{Cron: "* * * * *", Workdir: dir, SessionID: 4, Prompt: "missing session"},
	}
	for i := range rows {
		if err := store.db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("create invalid row: %v", err)
		}
	}

	s.cleanup()

	jobs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != valid.ID {
		t.Fatalf("after cleanup jobs = %#v, want only %d", jobs, valid.ID)
	}
}

// waitAttempt 等待调度器真正调用了一次 Dispatch（成功与否都算）。
func waitAttempt(t *testing.T, backend *fakeBackend) {
	t.Helper()
	select {
	case <-backend.attempt:
	case <-time.After(2 * time.Second):
		t.Fatalf("Dispatch was not called")
	}
}

// waitFireDone 等待任务的 fire goroutine 结束。
func waitFireDone(t *testing.T, s *Scheduler, id uint32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		running := s.inflight[id]
		s.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fire for job %d did not finish in time", id)
}

// TestSchedulerRunOnceRemovedAfterDelivery 一次性任务投递成功后立即删除（内存与数据库）。
func TestSchedulerRunOnceRemovedAfterDelivery(t *testing.T) {
	backend := newFakeBackend()
	s, store := testScheduler(t, backend)
	dir := t.TempDir()
	job, err := store.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 2, Prompt: "once", RunOnce: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.reload()
	s.mu.Lock()
	next := s.entries[job.ID].next
	s.mu.Unlock()

	s.tick(next)
	waitCall(t, backend)
	waitFireDone(t, s, job.ID)

	if _, err := store.Get(job.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("run-once job should be removed after delivery, Get err = %v", err)
	}
	s.mu.Lock()
	_, still := s.entries[job.ID]
	s.mu.Unlock()
	if still {
		t.Fatalf("run-once job still scheduled in memory")
	}
}

// TestSchedulerRunOnceKeptWhenBusy 一次性任务因会话忙碌被跳过时保留，等下一个触发点。
func TestSchedulerRunOnceKeptWhenBusy(t *testing.T) {
	backend := newFakeBackend()
	backend.busy = true
	s, store := testScheduler(t, backend)
	dir := t.TempDir()
	job, err := store.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 5, Prompt: "once busy", RunOnce: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.reload()
	s.mu.Lock()
	next := s.entries[job.ID].next
	s.mu.Unlock()

	s.tick(next)
	waitAttempt(t, backend)
	waitFireDone(t, s, job.ID)

	if _, err := store.Get(job.ID); err != nil {
		t.Fatalf("run-once job must be kept after a skipped (busy) delivery: %v", err)
	}
}

// TestSchedulerRunOnceKeptWhenSessionMissing 投递失败（目标失效）同样保留一次性任务。
func TestSchedulerRunOnceKeptWhenSessionMissing(t *testing.T) {
	backend := newFakeBackend()
	backend.missing[7] = true
	s, store := testScheduler(t, backend)
	dir := t.TempDir()
	job, err := store.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 7, Prompt: "once gone", RunOnce: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	s.reload()
	s.mu.Lock()
	next := s.entries[job.ID].next
	s.mu.Unlock()

	s.tick(next)
	waitAttempt(t, backend)
	waitFireDone(t, s, job.ID)

	if _, err := store.Get(job.ID); err != nil {
		t.Fatalf("run-once job must be kept when its target is missing: %v", err)
	}
}

// TestSchedulerPicksUpWritesFromAnotherStore 别的进程/实例写同一份 cron.sqlite 时，
// 调度器靠兜底重读（DefaultReloadInterval）拿到新任务——跨进程没有 Changed 通知。
func TestSchedulerPicksUpWritesFromAnotherStore(t *testing.T) {
	dir := t.TempDir()
	schedulerStore, err := Open(dir)
	if err != nil {
		t.Fatalf("open scheduler store: %v", err)
	}
	otherStore, err := Open(dir)
	if err != nil {
		t.Fatalf("open other store: %v", err)
	}
	t.Cleanup(func() { _ = otherStore.Close() })

	backend := newFakeBackend()
	s := newScheduler(schedulerStore, backend)
	s.reloadEvery = 20 * time.Millisecond
	s.started = true
	go s.run()
	t.Cleanup(s.Stop)

	job, err := otherStore.Add(Job{Cron: "* * * * *", Workdir: t.TempDir(), SessionID: 1, Prompt: "from another instance"})
	if err != nil {
		t.Fatalf("other store Add: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, ok := s.entries[job.ID]
		s.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scheduler did not pick up job %d written by another store", job.ID)
}

func TestStartAcquiresExclusiveLock(t *testing.T) {
	dir := t.TempDir()
	first, err := Start(dir, newFakeBackend())
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer first.Stop()

	if !first.Scheduling() {
		t.Fatal("first Start must own the lock and schedule")
	}
	if Default() != first.Store() {
		t.Fatal("first Start must register its store as the process default")
	}

	// 锁被占时仍然返回可用的调度器（只读写、不调度），工具层才有库可用。
	second, err := Start(dir, newFakeBackend())
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Start err = %v, want ErrLocked", err)
	}
	if second == nil {
		t.Fatal("second Start must still return a scheduler for tool writes")
	}
	if second.Scheduling() {
		t.Fatal("second Start must not schedule while another instance holds the lock")
	}
	job, err := second.Store().Add(Job{Cron: "@daily", Workdir: dir, SessionID: 1, Prompt: "from non-scheduling instance"})
	if err != nil {
		t.Fatalf("non-scheduling instance Add: %v", err)
	}
	second.Stop() // 不得清掉调度实例登记的默认存储
	if Default() == nil {
		t.Fatal("Stop of a non-scheduling instance cleared the default store")
	}
	if _, err := first.Store().Get(job.ID); err != nil {
		t.Fatalf("job written by non-scheduling instance not visible to the scheduler: %v", err)
	}

	first.Stop()
	if Default() != nil {
		t.Fatal("default store must be cleared after the scheduling instance stops")
	}
	third, err := Start(dir, newFakeBackend())
	if err != nil {
		t.Fatalf("Start after release: %v", err)
	}
	third.Stop()

	if _, err := os.Stat(filepath.Join(dir, DBFileName)); err != nil {
		t.Fatalf("cron.sqlite missing: %v", err)
	}
}
