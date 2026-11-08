package actions

import (
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/ui/state"
)

// connectionCloser 由 server/client 注入：强制关闭指定的客户端连接。
// actions 不直接依赖 connect（传输层实现由上层装配），未注入时退化为只回收会话。
var connectionCloser func(connIDs []uint64)

// SetConnectionCloser 注册强制断开连接的实施函数。
func SetConnectionCloser(fn func(connIDs []uint64)) {
	connectionCloser = fn
}

// forceGCDelay 会话释放完成后的等待时间：先把 loop goroutine / 回调 / 索引
// 的收尾对象真正变成不可达，再触发 GC，避免它们仍在栈上时统计不到收益。
const forceGCDelay = time.Second

// ForceGCResult 强制 GC 的执行结果（HTTP /gc 的响应体）。
type ForceGCResult struct {
	Success  bool `json:"success"`
	Released int  `json:"released"`
	Retained int  `json:"retained"`
}

// forceGCMu 串行化强制 GC：HTTP /gc 与 SIGUSR2 可能同时触发，
// 并发释放同一批会话会重复执行 join / 关库。
var forceGCMu sync.Mutex

// ForceGC 立即回收所有空闲会话，并在 1 秒后触发 GC、把空闲内存归还操作系统。
//
// 只保留仍有进行中工作的会话（状态非 Idle）；有客户端连接不影响回收——这些
// 连接会被强制断开（由 server/client 注入的 connectionCloser 关闭 WebSocket），
// 客户端重连后通过 session/resume 恢复。会话对象的定时释放通常要等
// SessionTimeout（默认 60s），空闲会话的 loop goroutine 与请求缓存会在此期间
// 一直驻留；这里不等超时，直接取消 loop 的 context（loop.Cancel）并释放全部
// 运行态资源。
//
// 释放是同步完成的，返回时这些会话已从注册表移除；GC 本身延迟到 forceGCDelay
// 之后在后台执行，不阻塞调用方（信号处理与 HTTP 请求都立刻返回）。
func ForceGC() ForceGCResult {
	forceGCMu.Lock()
	defer forceGCMu.Unlock()

	released, retained := releaseInactiveSessions()
	// debug.FreeOSMemory 内部会先执行一次 GC，再通过 madvise 把空闲页归还 OS；
	// 显式 runtime.GC 保证循环/栈上的临时对象也被回收后，FreeOSMemory 的
	// 统计与释放更彻底。
	time.AfterFunc(forceGCDelay, func() {
		runtime.GC()
		debug.FreeOSMemory()
	})
	return ForceGCResult{Success: true, Released: released, Retained: retained}
}

// sessionHasActiveTurn 判断会话是否还有进行中的工作。
//
// 只有完全空闲（Idle）的会话才允许强制回收：一旦状态离开 Idle，说明消息队列
// 刚派发、正在构建提示词、请求模型、执行工具或等待审批，回合上下文与响应缓冲
// 都仍在使用，提前 cancel 会中断任务（cron/background 回合尤其不能被误杀）。
// 「任务刚停止但 loop 还在待命」的会话状态就是 Idle，正是本函数要回收的目标。
func sessionHasActiveTurn(obj *sessionObj) bool {
	if obj == nil || obj.session == nil {
		return false
	}
	return obj.session.GetState() != state.StateIdle
}

// releaseInactiveSessions 摘除并释放所有空闲会话，返回（已释放, 保留）数量。
// 保留数量只包含仍有进行中工作的会话；有客户端连接不影响回收，相关连接会被
// 强制断开（客户端重连后经 session/resume 恢复）。
func releaseInactiveSessions() (int, int) {
	var toRelease []*sessionObj
	var ids []string
	var connCandidates []uint64
	seenConn := map[uint64]struct{}{}

	// 锁顺序必须与 registerConnCall 一致：sessionConnLock → sessLock。
	// 持有 sessionConnLock 期间摘除注册表、清理会话的连接映射并记录需要
	// 断开的连接，避免释放后广播仍尝试发往一个已不存在的会话。
	sessionConnLock.Lock()
	sessLock.Lock()
	for sessionID, obj := range sessions {
		if sessionHasActiveTurn(obj) {
			continue
		}
		// 停掉延迟释放定时器，避免摘除后又触发一次释放。
		if obj.releaseTimer != nil {
			obj.releaseTimer.Stop()
			obj.releaseTimer = nil
		}
		for _, connID := range sessionConnMap[sessionID] {
			if _, ok := seenConn[connID]; ok {
				continue
			}
			seenConn[connID] = struct{}{}
			connCandidates = append(connCandidates, connID)
		}
		delete(sessions, sessionID)
		delete(agentCallList, sessionID)
		delete(sessionConnMap, sessionID)
		toRelease = append(toRelease, obj)
		ids = append(ids, sessionID)
	}
	retained := len(sessions)
	sessLock.Unlock()
	sessionConnLock.Unlock()

	// 先断开客户端，再释放会话资源：客户端收到断连后重新连接并 session/resume，
	// 不会在释放过程中继续向旧会话发请求。
	if closer := connectionCloser; closer != nil && len(connCandidates) > 0 {
		if targets := connectionsToClose(connCandidates); len(targets) > 0 {
			logger.Info("force gc: disconnect %d client connection(s)", len(targets))
			closer(targets)
		}
	}

	for i, obj := range toRelease {
		logger.Info("force gc: release idle session %s", ids[i])
		releaseSessionResources(obj, ids[i])
	}
	return len(toRelease), retained
}

// connectionsToClose 从候选连接中挑出可以断开的连接：只断开不再绑定任何
// 存活会话的连接。一个 WebSocket 连接可以同时绑定多个会话，若它还绑着本次
// 未被回收的活跃会话（状态非 Idle），强行断开会误伤正常任务。
func connectionsToClose(candidates []uint64) []uint64 {
	// 先复制绑定关系再查询 sessions，避免持有 bindedSessionOnConnMu 时取 sessLock。
	binded := make(map[uint64][]string, len(candidates))
	bindedSessionOnConnMu.Lock()
	for _, connID := range candidates {
		binded[connID] = slices.Clone(bindedSessionOnConn[connID])
	}
	bindedSessionOnConnMu.Unlock()

	out := make([]uint64, 0, len(candidates))
	for _, connID := range candidates {
		keep := false
		for _, sessionID := range binded[connID] {
			if _, alive := lookupSession(sessionID); alive {
				keep = true
				break
			}
		}
		if !keep {
			out = append(out, connID)
		}
	}
	slices.Sort(out)
	return out
}
