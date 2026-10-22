package cronjob

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

var (
	// ErrLocked 同一配置目录已有其它进程在运行调度器。
	ErrLocked = errors.New("cronjob: another alkaid0 instance owns " + DBFileName)
	// ErrWorkdirMissing 任务指向的工作目录不存在（触发时删除该行）。
	ErrWorkdirMissing = errors.New("cronjob: workdir does not exist")
	// ErrSessionMissing 任务指向的会话不存在（触发时删除该行）。
	ErrSessionMissing = errors.New("cronjob: session does not exist")
	// ErrBusy 会话当前不空闲，本次触发跳过（不排队）。
	ErrBusy = errors.New("cronjob: session is busy")
)

const (
	// tickInterval 内存调度精度。表达式最小粒度是分钟，1s tick 足够且开销可忽略。
	tickInterval = time.Second
	// DefaultCleanupInterval 全量清理周期：删除失效任务（workdir/会话不存在、
	// 表达式非法、提示词为空）。
	DefaultCleanupInterval = 12 * time.Hour
	// DefaultReloadInterval 兜底重载周期。
	//
	// 变更通知（Store.Changed）只在同一进程内有效：别的 alkaid0 实例用 cron 工具 /
	// 私有协议往同一个 cron.sqlite 写入时，正在调度的进程收不到通知，内存表会一直
	// 停在旧状态。这里定期重读一次，让跨进程的增删在数秒内生效（表很小，开销可忽略）。
	DefaultReloadInterval = 10 * time.Second
)

// Backend 调度器与会话系统的唯一接口，由 server/actions 实现。
//
// 调度器不直接访问会话数据库：工作目录下的 .alkaid0/db.sqlite 归属会话层管理，
// 这里只做「是否存在」的询问与「投递提示词」的调用。
type Backend interface {
	// SessionExists 报告 workdir 下是否存在指定会话。
	// workdir 不存在时应返回 ErrWorkdirMissing。
	SessionExists(workdir string, sessionID uint32) (bool, error)
	// Dispatch 把提示词投递到会话。
	// 返回 ErrBusy 表示会话正在处理别的回合，本次触发应跳过。
	// 返回 ErrSessionMissing/ErrWorkdirMissing 表示该任务已失效，调度器会删除它。
	Dispatch(workdir string, sessionID uint32, prompt string) error
}

// entry 一条已解析的内存调度项。
type entry struct {
	job   Job
	sched cron.Schedule
	next  time.Time
}

// Scheduler cron 调度器。
//
// 时间语义：本地时区（cron.Schedule.Next 使用传入时间的 Location），因此
// “每天 9 点”指本机本地时间；服务停机期间错过的时间点不补跑，恢复后从当前
// 时刻重新计算下一次触发。
type Scheduler struct {
	store   *Store
	backend Backend
	lock    *fileLock

	// now 时钟注入点（测试用）。
	now          func() time.Time
	cleanupEvery time.Duration
	// reloadEvery 兜底重读周期（跨进程写入用）。
	reloadEvery time.Duration

	mu       sync.Mutex
	entries  map[uint32]*entry
	inflight map[uint32]bool

	quit     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	// started 标记 run 循环已启动（Stop 只等待真正启动过的循环）。
	started bool
}

// Start 打开 configDir/cron.sqlite，并在拿到独占锁时启动调度器。
//
// 独占锁只决定「谁负责调度」：同一配置目录只允许一个进程触发任务。没有拿到锁时
// 仍然打开数据库并登记为进程内默认存储（见 Default），返回 ErrLocked；此时
// 调用方可以照常使用 cron 工具读写任务，只是本进程不会触发它们。
// 这样多开实例时，任一实例都能创建/修改自己的定时任务，而不是只有主实例能用。
//
// 返回的 Scheduler（即使没在调度）需要由调用方持有；Stop 会关闭数据库并释放锁。
func Start(configDir string, backend Backend) (*Scheduler, error) {
	if backend == nil {
		return nil, fmt.Errorf("cronjob: backend is nil")
	}
	if strings.TrimSpace(configDir) == "" {
		return nil, fmt.Errorf("cronjob: config dir is empty")
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, fmt.Errorf("cronjob: create config dir %s: %w", configDir, err)
	}
	lock, lockErr := acquireFileLock(filepath.Join(configDir, LockFileName))
	if lockErr != nil && !errors.Is(lockErr, ErrLocked) {
		return nil, lockErr
	}
	store, err := Open(configDir)
	if err != nil {
		if lock != nil {
			lock.release()
		}
		return nil, err
	}
	s := newScheduler(store, backend)
	// 无论是否负责调度都登记：工具与 @cron 注入只认这里。
	// 用 registerDefault 而不是覆盖：同进程里已在调度的实例优先级更高。
	registerDefault(store)
	if lockErr != nil {
		logger.Info("cronjob: %s is held by another instance; scheduling disabled in this process (cron tool still works, db=%s)",
			LockFileName, store.Path())
		return s, ErrLocked
	}
	s.lock = lock
	s.reload()
	s.started = true
	go s.run()
	logger.Info("cronjob scheduler started (%d job(s), db=%s)", s.JobCount(), store.Path())
	return s, nil
}

// Store 返回调度器持有的存储（装配与测试用）。
func (s *Scheduler) Store() *Store {
	if s == nil {
		return nil
	}
	return s.store
}

// Scheduling 报告本进程是否在调度任务（false 表示只读写、不触发）。
func (s *Scheduler) Scheduling() bool {
	if s == nil {
		return false
	}
	return s.started
}

// newScheduler 构造调度器（不启动后台循环，测试可直接调用 tick/cleanup）。
func newScheduler(store *Store, backend Backend) *Scheduler {
	return &Scheduler{
		store:        store,
		backend:      backend,
		now:          time.Now,
		cleanupEvery: DefaultCleanupInterval,
		reloadEvery:  DefaultReloadInterval,
		entries:      make(map[uint32]*entry),
		inflight:     make(map[uint32]bool),
		quit:         make(chan struct{}),
		done:         make(chan struct{}),
	}
}

// Stop 停止调度循环、注销默认存储、关闭数据库并释放独占锁。
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	started := s.started
	s.stopOnce.Do(func() { close(s.quit) })
	if started {
		<-s.done
	}
	if s.store != nil {
		// 只在默认存储就是自己时清空：非调度实例不能把调度实例的登记抹掉。
		clearDefault(s.store)
		_ = s.store.Close()
	}
	if s.lock != nil {
		s.lock.release()
	}
}

// JobCount 返回当前内存中的任务数。
func (s *Scheduler) JobCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// run 调度主循环。
//
// 启动时先做一次全量清理（不阻塞服务启动：Start 里 go run 之后立即返回），之后
// 每 cleanupEvery 清理一次；任务增删通过 store 的变更通知即时重载。
func (s *Scheduler) run() {
	defer close(s.done)
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	clean := time.NewTicker(s.cleanupEvery)
	defer clean.Stop()
	// 兜底重读：别的进程写 cron.sqlite 时本进程收不到 Changed 通知。
	reload := time.NewTicker(s.reloadEvery)
	defer reload.Stop()

	s.cleanup()

	for {
		select {
		case <-s.quit:
			return
		case <-tick.C:
			s.tick(s.now())
		case <-clean.C:
			s.cleanup()
		case <-reload.C:
			s.reload()
		case <-s.store.Changed():
			s.reload()
		}
	}
}

// reload 从数据库重新构建内存调度表。
//
// 下一次触发时间统一从当前时刻计算：不保留旧值，也就天然满足“停机/暂停期间错过
// 的时间点不补跑”。表达式非法的行会被跳过（下一次清理删除）。
func (s *Scheduler) reload() {
	jobs, err := s.store.List()
	if err != nil {
		logger.Warn("cronjob: reload jobs: %v", err)
		return
	}
	now := s.now()
	entries := make(map[uint32]*entry, len(jobs))
	for _, j := range jobs {
		sched, err := cron.ParseStandard(strings.TrimSpace(j.Cron))
		if err != nil {
			logger.Warn("cronjob %d: skip invalid cron %q: %v", j.ID, j.Cron, err)
			continue
		}
		next := sched.Next(now)
		if next.IsZero() {
			logger.Warn("cronjob %d: no future occurrence for %q", j.ID, j.Cron)
			continue
		}
		entries[j.ID] = &entry{job: j, sched: sched, next: next}
	}
	s.mu.Lock()
	s.entries = entries
	s.mu.Unlock()
}

// tick 触发所有已到期任务。
//
// 每个任务在单次 tick 内最多触发一次；下一次触发时间从 now 重新计算，所以即使
// 服务阻塞了一段时间也不会连发多次。触发在独立 goroutine 中执行，慢的会话加载
// 不会拖住其它任务；同一任务上一次尚未结束时本次跳过。
func (s *Scheduler) tick(now time.Time) {
	var fires []Job
	s.mu.Lock()
	for _, e := range s.entries {
		if e.next.IsZero() || e.next.After(now) {
			continue
		}
		fires = append(fires, e.job)
		e.next = e.sched.Next(now)
	}
	s.mu.Unlock()

	for _, j := range fires {
		go s.fire(j)
	}
}

// fire 投递一条任务。
func (s *Scheduler) fire(j Job) {
	if !s.beginFire(j.ID) {
		logger.Debug("cronjob %d: previous run still in flight, skip", j.ID)
		return
	}
	defer s.endFire(j.ID)

	err := s.backend.Dispatch(j.Workdir, j.SessionID, j.Prompt)
	switch {
	case err == nil:
		logger.Info("cronjob %d fired (%s): session %d @ %s", j.ID, j.Cron, j.SessionID, j.Workdir)
		// 一次性任务：投递成功即消费掉，不再参与后续调度。
		if j.RunOnce {
			logger.Info("cronjob %d is run-once, removed after delivery", j.ID)
			s.removeJobs([]uint32{j.ID})
		}
	case errors.Is(err, ErrBusy):
		// 无论是否 run-once，忙碌都只跳过本次，等下一个触发点。
		logger.Info("cronjob %d skipped: session %d is busy", j.ID, j.SessionID)
	case errors.Is(err, ErrWorkdirMissing):
		s.failInvalidTarget(j, fmt.Sprintf("workdir %s no longer exists", j.Workdir))
	case errors.Is(err, ErrSessionMissing):
		s.failInvalidTarget(j, fmt.Sprintf("session %d no longer exists in %s", j.SessionID, j.Workdir))
	default:
		logger.Warn("cronjob %d dispatch failed: %v", j.ID, err)
	}
}

// failInvalidTarget 处理「目标已失效」导致的投递失败。
//
// 普通任务按第一阶段约定立即删除；一次性任务遵循「投递失败不删，等下次」：
// 保留该行，下一个触发点再试（目标确实失效时由 12h 清理统一删除）。
func (s *Scheduler) failInvalidTarget(j Job, reason string) {
	if j.RunOnce {
		logger.Warn("cronjob %d (run once) not delivered: %s; kept for the next trigger", j.ID, reason)
		return
	}
	logger.Warn("cronjob %d removed: %s", j.ID, reason)
	s.removeJobs([]uint32{j.ID})
}

// beginFire 标记任务正在执行；已在执行时返回 false。
func (s *Scheduler) beginFire(id uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[id] {
		return false
	}
	s.inflight[id] = true
	return true
}

// endFire 清除执行标记。
func (s *Scheduler) endFire(id uint32) {
	s.mu.Lock()
	delete(s.inflight, id)
	s.mu.Unlock()
}

// cleanup 删除全部失效任务：表达式非法、提示词为空、工作目录不存在、会话不存在。
//
// 「会话不存在」的判定交给 Backend（它负责打开对应工作目录的会话库）；检查过程
// 出错（例如数据库被占用）时保守保留，等下一轮清理重试。
func (s *Scheduler) cleanup() {
	jobs, err := s.store.List()
	if err != nil {
		logger.Warn("cronjob: cleanup list jobs: %v", err)
		return
	}
	var ids []uint32
	for _, j := range jobs {
		reason := s.invalidReason(j)
		if reason == "" {
			continue
		}
		logger.Info("cronjob %d removed: %s", j.ID, reason)
		ids = append(ids, j.ID)
	}
	if len(ids) == 0 {
		return
	}
	s.removeJobs(ids)
}

// invalidReason 返回任务失效原因；有效时返回空串。
func (s *Scheduler) invalidReason(j Job) string {
	if strings.TrimSpace(j.Prompt) == "" {
		return "empty prompt"
	}
	if _, err := cron.ParseStandard(strings.TrimSpace(j.Cron)); err != nil {
		return fmt.Sprintf("invalid cron expression %q", j.Cron)
	}
	if strings.TrimSpace(j.Workdir) == "" {
		return "empty workdir"
	}
	if fi, err := os.Stat(j.Workdir); err != nil || !fi.IsDir() {
		return fmt.Sprintf("workdir %s not found", j.Workdir)
	}
	ok, err := s.backend.SessionExists(j.Workdir, j.SessionID)
	if err != nil {
		// 检查失败不等于失效：保留任务，下一轮清理重试。
		logger.Warn("cronjob %d: check session %d: %v", j.ID, j.SessionID, err)
		return ""
	}
	if !ok {
		return fmt.Sprintf("session %d not found in %s", j.SessionID, j.Workdir)
	}
	return ""
}

// removeJobs 从数据库与内存表中同时删除任务。
func (s *Scheduler) removeJobs(ids []uint32) {
	if len(ids) == 0 {
		return
	}
	if _, err := s.store.DeleteMany(ids); err != nil {
		logger.Warn("cronjob: delete jobs %v: %v", ids, err)
		return
	}
	s.mu.Lock()
	for _, id := range ids {
		delete(s.entries, id)
		delete(s.inflight, id)
	}
	s.mu.Unlock()
}
