package actions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/cronjob"
	"github.com/cxykevin/alkaid0/hooks"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
)

// cronBackend 把 cronjob 的「投递 / 会话存在性检查」接到会话系统上。
//
// 调度器本身不碰会话库：工作目录下 .alkaid0/db.sqlite 的连接、会话加载、消息
// 落库与广播全部由本包完成，行为与客户端发一条普通用户消息完全一致。
type cronBackend struct{}

// SessionExists 报告 workdir 下是否存在指定会话（chat 行）。
func (cronBackend) SessionExists(workdir string, sessionID uint32) (bool, error) {
	return chatExists(workdir, sessionID)
}

// Dispatch 把 cron 提示词投递到会话。
func (cronBackend) Dispatch(workdir string, sessionID uint32, prompt string) error {
	return dispatchCronPrompt(workdir, sessionID, prompt)
}

var (
	cronStartOnce sync.Once
	// cronScheduler 持有调度器引用：进程生命周期内常驻，不回收。
	//
	// 非调度实例（锁被别的进程持有）同样需要持有它——cron 工具与 @cron 注入通过
	// cronjob.Default() 拿存储，这里保证那个存储不会被提前关闭。
	cronScheduler *cronjob.Scheduler
)

// StartCron 打开配置目录下的 cron.sqlite 并启动 cron 调度器。
//
// 由 server/client 在 InitFuncs 之后调用一次：此时 rpcSrv 已就绪，cron 触发的
// 回合遇到需要审批的工具调用时，可以正常向会话连接发 request_permission。
// 独占锁被其它进程持有时，本进程不调度但仍可读写任务（cron 工具照常可用）。
func StartCron() {
	cronStartOnce.Do(func() {
		dir := filepath.Dir(configutil.ExpandPath(config.Path()))
		sched, err := cronjob.Start(dir, cronBackend{})
		if sched != nil {
			cronScheduler = sched
		}
		if err != nil {
			if errors.Is(err, cronjob.ErrLocked) {
				logger.Info("cronjob: another instance schedules %s; this process only serves cron tool read/write",
					filepath.Join(dir, cronjob.DBFileName))
				return
			}
			logger.Warn("cronjob start failed: %v", err)
		}
	})
}

// sessionDBPath 返回会话数据库在指定工作目录下的路径。
//
// 与 loadDB -> storage.InitStorage 的默认值保持一致（.alkaid0/db.sqlite，
// 调试用的 ALKAID0_DEBUG_SQLITEFILE 可改文件名）。
func sessionDBPath(cwd string) string {
	name := "db.sqlite"
	if v := os.Getenv("ALKAID0_DEBUG_SQLITEFILE"); v != "" {
		name = v
	}
	return filepath.Join(cwd, ".alkaid0", name)
}

// chatExists 判断 workdir 下是否存在指定会话。
//
// 先看会话库文件是否存在：不存在的目录不为“检查一下”而创建 .alkaid0 目录与空库。
// workdir 不存在时返回 cronjob.ErrWorkdirMissing，由调度器删除对应任务。
func chatExists(workdir string, chatID uint32) (bool, error) {
	cwd := filepath.Clean(workdir)
	fi, err := os.Stat(cwd)
	if err != nil || !fi.IsDir() {
		return false, cronjob.ErrWorkdirMissing
	}
	if dbfi, err := os.Stat(sessionDBPath(cwd)); err != nil || dbfi.IsDir() {
		return false, nil
	}
	db, err := loadDB(cwd)
	if err != nil {
		return false, err
	}
	defer closeDB(cwd)

	var n int64
	if err := db.Model(&structs.Chats{}).Where("id = ?", chatID).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// dispatchCronPrompt 把一条 cron 提示词投递到会话。
//
// 语义（用户 2026-10-21 确认）：
//   - 提示词作为普通用户消息落库并广播，正文前加 cronjob.Prefix；
//   - 会话正在处理其它回合时直接跳过本次触发，不排队（ErrBusy）；
//   - 工作目录/会话不存在时返回对应错误，调度器删除该任务；
//   - 触发的回合可能停在等待审批上：保持会话存活，直到有人审批或回合结束。
func dispatchCronPrompt(workdir string, chatID uint32, prompt string) error {
	cwd := filepath.Clean(workdir)
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("cronjob: prompt is empty")
	}
	exists, err := chatExists(cwd, chatID)
	if err != nil {
		return err
	}
	if !exists {
		return cronjob.ErrSessionMissing
	}

	sessionID := cwd2SessionID(cwd, chatID)
	_, wasLoaded := lookupSession(sessionID)

	id := chatID
	sess, err := loadSession(cwd, &id, true)
	if err != nil {
		return fmt.Errorf("cronjob: load session: %w", err)
	}
	sessionID = cwd2SessionID(cwd, id)

	// 冷加载：持久化的非空闲状态只可能来自上一个进程（退出/崩溃时停在请求或审批
	// 中），新 loop 里没有任何在途回合。先复位，否则这条 cron 会永远被判为忙碌。
	// 有客户端连接时不动状态：并发 resume 的客户端可能已经开始新回合，此时的状态
	// 属于活 loop，不能按陈旧数据处理。
	if !wasLoaded && sess.GetState() != state.StateIdle && !sessionHasConns(sessionID) {
		if err := sess.SaveState(state.StateIdle); err != nil {
			logger.Warn("cronjob: reset stale state of session %d: %v", id, err)
			sess.SetState(state.StateIdle)
		}
	}
	if sess.GetState() != state.StateIdle {
		// 本次 loadSession 增加了引用：交给延迟释放回收（有连接时会自动跳过）。
		scheduleSessionRelease(sessionID)
		return cronjob.ErrBusy
	}

	obj, ok := lookupSession(sessionID)
	if !ok || obj.msgQueue == nil {
		scheduleSessionRelease(sessionID)
		return fmt.Errorf("cronjob: session %s is not available", sessionID)
	}

	// 先标记 cron 保活，再入队：否则回合可能在标记之前就结束，loop 回调清掉的
	// 标记会被随后重新置位，会话就再也等不到释放。回合结束时回调会清除标记，
	// 延迟释放随后按正常空闲语义回收会话（见 scheduleSessionRelease）。
	holdCronSession(sessionID)
	if _, err := obj.msgQueue.enqueue(msgQueueKindPrompt, "", "", cronjob.Prefix+prompt); err != nil {
		releaseCronHold(sessionID)
		return fmt.Errorf("cronjob: enqueue prompt: %w", err)
	}
	obj.msgQueue.wakeup()
	// 会话已被 cron 唤醒：同步执行 OnCronjobWakeup 钩子（阻塞到命令退出），
	// 钩子拿到 SESSION_ID / SESSION_PATH，可用于外部通知或拉起配套服务。
	hooks.Run(hooks.EventCronjobWakeup, id, cwd)
	scheduleSessionRelease(sessionID)
	return nil
}

// sessionHasConns 报告当前是否有客户端连接绑定在该会话上。
func sessionHasConns(sessionID string) bool {
	sessionConnLock.Lock()
	defer sessionConnLock.Unlock()
	return len(sessionConnMap[sessionID]) > 0
}

// holdCronSession 标记会话由 cron 触发、直到本回合结束都不释放。
func holdCronSession(sessionID string) {
	setCronHold(sessionID, true)
}

// releaseCronHold 在投递失败时撤销保活标记，避免会话永远不被释放。
func releaseCronHold(sessionID string) {
	setCronHold(sessionID, false)
}

func setCronHold(sessionID string, hold bool) {
	sessLock.Lock()
	defer sessLock.Unlock()
	if obj, ok := sessions[sessionID]; ok {
		obj.cronHold = hold
	}
}
