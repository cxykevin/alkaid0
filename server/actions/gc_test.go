package actions

import (
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/loop"
	"github.com/cxykevin/alkaid0/ui/state"
)

// newGCTestSession 在会话注册表里放一个会话，返回其规范 ID 与对象。
// cleanup 负责删除注册表条目（ForceGC 可能已经删除，重复删除无副作用）。
func newGCTestSession(t *testing.T, cwd string, id uint32, st state.State) (string, *sessionObj) {
	t.Helper()
	sessID := cwd2SessionID(cwd, id)
	chat := &structs.Chats{ID: id}
	chat.SetState(st)
	obj := &sessionObj{cwd: cwd, id: id, session: chat}
	sessLock.Lock()
	sessions[sessID] = obj
	agentCallList[sessID] = map[string]func(){}
	sessLock.Unlock()
	t.Cleanup(func() {
		sessLock.Lock()
		delete(sessions, sessID)
		delete(agentCallList, sessID)
		sessLock.Unlock()
		sessionConnLock.Lock()
		delete(sessionConnMap, sessID)
		sessionConnLock.Unlock()
		bindedSessionOnConnMu.Lock()
		delete(bindedSessionOnConn, 9001)
		bindedSessionOnConnMu.Unlock()
	})
	return sessID, obj
}

// captureConnectionCloser 安装一个记录调用的假连接断开器，测试结束后恢复。
func captureConnectionCloser(t *testing.T) *[][]uint64 {
	t.Helper()
	calls := &[][]uint64{}
	old := connectionCloser
	SetConnectionCloser(func(ids []uint64) {
		*calls = append(*calls, append([]uint64(nil), ids...))
	})
	t.Cleanup(func() { connectionCloser = old })
	return calls
}

func closerCalledWith(calls *[][]uint64, connID uint64) bool {
	for _, ids := range *calls {
		for _, id := range ids {
			if id == connID {
				return true
			}
		}
	}
	return false
}

// TestForceGCDisconnectsClientsOfRecycledSessions 验证强制 GC 不把「有客户端
// 连接」当作豁免：所有空闲（Idle）会话的 loop 都会立即回收，相关连接被强制
// 断开（客户端重连后 resume）；正在工作的会话保留。
func TestForceGCDisconnectsClientsOfRecycledSessions(t *testing.T) {
	calls := captureConnectionCloser(t)
	cwd := t.TempDir()
	idleID, idleObj := newGCTestSession(t, cwd, 101, state.StateIdle)
	busyID, _ := newGCTestSession(t, cwd, 102, state.StateRequesting)
	connID, _ := newGCTestSession(t, cwd, 103, state.StateIdle)

	// loop goroutine 仍在待命的空闲会话：强制回收必须取消它的生命周期 ctx。
	idleObj.loop = loop.New(idleObj.session)

	// 一个在线客户端绑定在空闲会话上。
	sessionConnLock.Lock()
	sessionConnMap[connID] = []uint64{9001}
	sessionConnLock.Unlock()
	bindedSessionOnConnMu.Lock()
	bindedSessionOnConn[9001] = []string{connID}
	bindedSessionOnConnMu.Unlock()

	res := ForceGC()
	if !res.Success {
		t.Fatalf("ForceGC().Success = false, want true")
	}
	if res.Released < 2 {
		t.Errorf("ForceGC().Released = %d, want >= 2 (idle + connected idle)", res.Released)
	}
	if res.Retained < 1 {
		t.Errorf("ForceGC().Retained = %d, want >= 1 (busy)", res.Retained)
	}

	if _, ok := lookupSession(idleID); ok {
		t.Errorf("空闲无连接会话 %s 应被强制回收", idleID)
	}
	if _, ok := lookupSession(connID); ok {
		t.Errorf("空闲会话 %s 即使有客户端连接也应被强制回收", connID)
	}
	if _, ok := lookupSession(busyID); !ok {
		t.Errorf("正在请求的会话 %s 不应被回收", busyID)
	}

	if !closerCalledWith(calls, 9001) {
		t.Errorf("回收有连接的会话后应强制断开该客户端连接 9001，calls = %v", *calls)
	}

	// loop 的 ctx 已被结束（Done 通道已关闭）。
	select {
	case <-idleObj.loop.Done():
	default:
		t.Errorf("强制回收应结束会话 loop 的生命周期 ctx")
	}

	// 运行时广播映射清理。
	sessionConnLock.Lock()
	_, mapped := sessionConnMap[connID]
	sessionConnLock.Unlock()
	if mapped {
		t.Errorf("强制回收后 sessionConnMap[%s] 应被删除", connID)
	}

	// 会话级 agent 调用表一并清理。
	sessLock.Lock()
	_, inCallList := agentCallList[idleID]
	sessLock.Unlock()
	if inCallList {
		t.Errorf("强制回收后 agentCallList[%s] 应被删除", idleID)
	}
}

// TestForceGCKeepsConnectionWithActiveSession 验证一个连接同时绑定多个会话时，
// 只要还有未被回收的活跃会话，就不能断开该连接，避免误伤正常任务。
func TestForceGCKeepsConnectionWithActiveSession(t *testing.T) {
	calls := captureConnectionCloser(t)
	cwd := t.TempDir()
	idleID, _ := newGCTestSession(t, cwd, 201, state.StateIdle)
	busyID, _ := newGCTestSession(t, cwd, 202, state.StateRequesting)

	sessionConnLock.Lock()
	sessionConnMap[idleID] = []uint64{9001}
	sessionConnMap[busyID] = []uint64{9001}
	sessionConnLock.Unlock()
	bindedSessionOnConnMu.Lock()
	bindedSessionOnConn[9001] = []string{idleID, busyID}
	bindedSessionOnConnMu.Unlock()

	ForceGC()

	if _, ok := lookupSession(idleID); ok {
		t.Errorf("空闲会话 %s 应被强制回收", idleID)
	}
	if _, ok := lookupSession(busyID); !ok {
		t.Errorf("正在请求的会话 %s 不应被回收", busyID)
	}
	if closerCalledWith(calls, 9001) {
		t.Errorf("连接 9001 仍绑定活跃会话 %s，不应被断开，calls = %v", busyID, *calls)
	}
}
