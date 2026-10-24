package actions

import (
	"path"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestACPStateOf 会话内部状态到 ACP state_update.state 的映射。
// 客户端（alcoh）的 Esc 只在 running/requires_action 时才发送 session/cancel，
// 映射错误会直接导致停止键失效。
func TestACPStateOf(t *testing.T) {
	cases := []struct {
		in   state.State
		want string
	}{
		{state.StateIdle, "idle"},
		{state.StateWaitApprove, "requires_action"},
		{state.StateWaiting, "running"},
		{state.StateGeneratingPrompt, "running"},
		{state.StateRequesting, "running"},
		{state.StateReciving, "running"},
		{state.StateToolCalling, "running"},
	}
	for _, c := range cases {
		if got := acpStateOf(c.in); got != c.want {
			t.Errorf("acpStateOf(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSessionResumeSyncsRunningState 回归：resume 一个仍在工作的会话时，
// 新连接必须收到 state_update(running)。
//
// 背景：state_update 是纯事件，不会随 resume 重放。之前一轮由旧连接发起、
// alcoh 重连后 resume（例如客户端重启或切回会话），新连接从未收到 running，
// 界面显示"已结束"，而服务端 loop 其实还在跑；Esc 停止因为客户端认为空闲
// 而根本不会发出 session/cancel（实测于 alcoh）。
func TestSessionResumeSyncsRunningState(t *testing.T) {
	// 备份并清空全局注册表，模拟独立会话
	sessLock.Lock()
	oldSessions := sessions
	sessions = map[string]*sessionObj{}
	sessLock.Unlock()
	dbLock.Lock()
	oldDbs := dbs
	dbs = map[string]*dbObj{}
	dbLock.Unlock()
	oldBinded := bindedSessionOnConn
	oldConnCall := connCallMap
	oldSessionConn := sessionConnMap
	bindedSessionOnConn = map[uint64][]string{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	sessionConnMap = map[string][]uint64{}
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		dbLock.Lock()
		dbs = oldDbs
		dbLock.Unlock()
		bindedSessionOnConn = oldBinded
		connCallMap = oldConnCall
		sessionConnMap = oldSessionConn
	}()

	if config.GlobalConfig == nil {
		config.GlobalConfigSwap(cfgStructs.Config{})
	}

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = codebase.CloseDirectory(tmpDir)
	})
	db, err := storage.InitStorage(path.Join(tmpDir, ".alkaid0"), "")
	if err != nil {
		t.Fatalf("InitStorage failed: %v", err)
	}
	defer u.Unwrap(db.DB()).Close()
	id, err := funcs.CreateChat(db)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	sessionID := cwd2SessionID(tmpDir, id)

	noop := func(string, any, *string) error { return nil }
	// 冷加载（会话进入内存注册表）
	if _, err := SessionResume(SessionResumeRequest{Cwd: tmpDir, SessionID: sessionID}, noop, 1); err != nil {
		t.Fatalf("cold SessionResume failed: %v", err)
	}
	sessLock.Lock()
	obj := sessions[sessionID]
	sessLock.Unlock()
	if obj == nil || obj.session == nil {
		t.Fatal("session should be registered after resume")
	}
	sess := obj.session
	if obj.indexDone != nil {
		select {
		case <-obj.indexDone:
		case <-time.After(10 * time.Second):
			t.Error("timeout waiting for async index goroutine")
		}
	}
	defer closeSession(sessionID)

	// 模拟"回合仍在跑"：状态置为 Requesting（旧连接断开后 loop 会继续）。
	sess.SetState(state.StateRequesting)

	// 新连接 resume（热路径）：收集本连接收到的 update。
	var gotStates []string
	record := func(method string, params any, id *string) error {
		if method != "session/update" {
			return nil
		}
		update, ok := params.(SessionUpdate)
		if !ok {
			return nil
		}
		if uu, ok := update.Update.(SessionUpdateUpdate); ok && uu.SessionUpdate == "state_update" {
			gotStates = append(gotStates, uu.State)
		}
		return nil
	}
	if _, err := SessionResume(SessionResumeRequest{Cwd: tmpDir, SessionID: sessionID}, record, 2); err != nil {
		t.Fatalf("hot SessionResume failed: %v", err)
	}

	if len(gotStates) == 0 {
		t.Fatal("session/resume did not sync the current state to the new connection")
	}
	if gotStates[0] != "running" {
		t.Fatalf("first synced state = %q, want running (states=%v)", gotStates[0], gotStates)
	}

	// 空闲时应同步 idle，保证状态回落同样能被客户端感知。
	gotStates = nil
	sess.SetState(state.StateIdle)
	if _, err := SessionResume(SessionResumeRequest{Cwd: tmpDir, SessionID: sessionID}, record, 3); err != nil {
		t.Fatalf("hot SessionResume (idle) failed: %v", err)
	}
	if len(gotStates) == 0 || gotStates[0] != "idle" {
		t.Fatalf("synced state for idle session = %v, want idle", gotStates)
	}

	// 冷加载时排队的遗留消息不应在连接注册前被派发：完成一次 resume 后队列为空。
	// （此处只验证 wakeup 被调用后 worker 不会 panic/死锁，具体派发由 msgqueue 测试覆盖。）
}
