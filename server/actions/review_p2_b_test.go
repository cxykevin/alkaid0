package actions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/loop"
	u "github.com/cxykevin/alkaid0/utils"
)

// newResumedSession 创建带 .alkaid0 的工作区与会话，并通过 session/resume
// 冷还原为一个带真实 loop 的会话（同时把 connID 绑定到该会话）。
func newResumedSession(t *testing.T, connID uint64) (string, uint32, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("create .alkaid0: %v", err)
	}
	db, err := storage.InitStorage(filepath.Join(dir, ".alkaid0"), "")
	if err != nil {
		t.Fatalf("InitStorage: %v", err)
	}
	t.Cleanup(func() { _ = u.Unwrap(db.DB()).Close() })
	// resume 会启动异步索引，打开 dir/.alkaid0/codebase.sqlite。Windows 上未关闭的
	// 句柄会让 t.TempDir() 的清理失败（unlinkat ... being used by another process）。
	t.Cleanup(func() { _ = codebase.CloseDirectory(dir) })
	id, err := funcs.CreateChat(db, false)
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	sessionID := cwd2SessionID(dir, id)
	call := func(string, any, *string) error { return nil }
	if _, err := SessionResume(SessionResumeRequest{Cwd: dir, SessionID: sessionID}, call, connID); err != nil {
		t.Fatalf("SessionResume: %v", err)
	}
	obj, ok := lookupSession(sessionID)
	if !ok {
		t.Fatal("session should be registered after resume")
	}
	if obj.indexDone != nil {
		select {
		case <-obj.indexDone:
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for async index goroutine")
		}
	}
	return dir, id, sessionID
}

// ---- A7: activeAgentMessages 必须在本轮结束时清空 ----

func TestActiveAgentMessagesClearedOnTurnEnd(t *testing.T) {
	_, _, sessionID := newResumedSession(t, 1)
	obj, ok := lookupSession(sessionID)
	if !ok {
		t.Fatal("session lost")
	}

	// 一轮流式输出（产生活跃条目），随后用户取消结束本轮（取消回调的 MsgID 为 0）
	obj.loopCallback(loop.AIResponse{MsgID: 42, Content: "partial"})
	obj.loopCallback(loop.AIResponse{StopReason: loop.StopReasonUser})

	obj.streamMu.Lock()
	remaining := len(obj.activeAgentMessages)
	obj.streamMu.Unlock()
	if remaining != 0 {
		t.Errorf("轮次结束后 activeAgentMessages 必须清空（取消回调 MsgID=0 时旧实现只删 resp.MsgID，条目永久残留），仍剩 %d 条", remaining)
	}
	closeSession(sessionID)
}

// ---- A8: loadDB/closeDB 缓存键必须规范化 ----

func TestLoadDBKeyNormalization(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("create .alkaid0: %v", err)
	}

	db1, err := loadDB(dir)
	if err != nil {
		t.Fatalf("loadDB(dir): %v", err)
	}
	db2, err := loadDB(dir + string(filepath.Separator))
	if err != nil {
		t.Fatalf("loadDB(dir+separator): %v", err)
	}
	if db1 != db2 {
		t.Error("同一目录的两种写法必须复用同一连接（旧实现按未 Clean 的键各建一个连接，旧连接泄漏）")
	}

	closeDB(dir)
	closeDB(dir + string(filepath.Separator))
	dbLock.Lock()
	_, leaked := dbs[dbKey(dir)]
	dbLock.Unlock()
	if leaked {
		t.Error("引用计数归零后连接必须已关闭并从缓存移除")
	}
}

// ---- A9: unregisterConnCall 不得误删连接级 call ----

func TestUnregisterConnCallKeepsConnectionLevelCall(t *testing.T) {
	oldConnCall, oldSessionConn := connCallMap, sessionConnMap
	connCallMap = map[uint64]func(string, any, *string) error{}
	sessionConnMap = map[string][]uint64{}
	defer func() { connCallMap, sessionConnMap = oldConnCall, oldSessionConn }()

	const connID = 555
	sidA, sidB := "sess_1:/tmp/unregister-a", "sess_2:/tmp/unregister-b"
	fn := func(string, any, *string) error { return nil }
	registerConnCall(connID, sidA, fn)
	registerConnCall(connID, sidB, fn)

	unregisterConnCall(connID, sidA)
	connCallLock.Lock()
	_, kept := connCallMap[connID]
	connCallLock.Unlock()
	if !kept {
		t.Error("连接仍绑定会话 B 时不得删除连接级 call（否则会话 B 的所有广播都会丢失）")
	}
	sessionConnLock.Lock()
	boundB := len(sessionConnMap[sidB]) == 1 && sessionConnMap[sidB][0] == connID
	sessionConnLock.Unlock()
	if !boundB {
		t.Error("解绑会话 A 不应影响会话 B 的连接绑定")
	}

	// 最后一个会话也解绑后才删除连接级 call
	unregisterConnCall(connID, sidB)
	connCallLock.Lock()
	_, kept = connCallMap[connID]
	connCallLock.Unlock()
	if kept {
		t.Error("连接不再绑定任何会话时应删除其 call")
	}
}

// ---- A10: 释放会话必须 join loop 后再关 DB ----

func TestCloseSessionJoinsLoopBeforeClosingDB(t *testing.T) {
	oldTimeout := loopJoinTimeout
	loopJoinTimeout = 5 * time.Second
	defer func() { loopJoinTimeout = oldTimeout }()

	oldSessions, oldAgentCallList := sessions, agentCallList
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
	}()

	dir := t.TempDir()
	obj := &sessionObj{
		cwd:     dir,
		id:      99991,
		session: &structs.Chats{ID: 99991, ReferCount: 1},
	}
	obj.loop = loop.New(obj.session)
	sessionID := cwd2SessionID(dir, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = map[string]func(){}

	// 模拟一个仍在运行的 loop goroutine（旧实现先 closeDB 再返回）
	exited := make(chan struct{})
	obj.loopWG.Add(1)
	go func() {
		defer obj.loopWG.Done()
		defer close(exited)
		time.Sleep(300 * time.Millisecond)
	}()

	closeSession(sessionID)

	select {
	case <-exited:
		// 正确：closeSession 等到了 loop 退出
	default:
		t.Error("closeSession 在 loop goroutine 退出前就返回了（DB 可能已被关闭，存在 use-after-close）")
	}
	// 释放流程开始后不得再启动新 loop（否则 Wait 与 Add 并发）
	obj.startSessionLoop(loop.New(obj.session))
}

// ---- A11: session/delete 与 session/update 不得信任 sessionId 中的 cwd ----

func TestSessionDeleteRejectsForgedCwd(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 1)
	id := ids[0]

	// 1) 非规范 cwd（尾随 "/."）：不得删除真实会话
	forged := cwd2SessionID(dir+string(filepath.Separator)+".", id)
	if _, err := SessionDelete(SessionDeleteRequest{SessionID: forged}, nil, 1); err != nil {
		t.Fatalf("非规范 sessionId 应按静默成功处理: %v", err)
	}
	if _, err := funcs.QueryChat(db, id); err != nil {
		t.Error("伪造 cwd 的 session/delete 不得删除真实会话")
	}

	// 2) 任意已存在但未初始化 .alkaid0 的目录：不得被用作会话库
	victim := t.TempDir()
	sid2 := cwd2SessionID(victim, id)
	if _, err := SessionDelete(SessionDeleteRequest{SessionID: sid2}, nil, 1); err != nil {
		t.Fatalf("未知 cwd 应按静默成功处理: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(victim, ".alkaid0")); !os.IsNotExist(statErr) {
		t.Error("session/delete 不得凭 sessionId 在任意目录创建 .alkaid0 会话库")
	}
}

func TestHandleSessionUpdateRejectsForgedCwd(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 1)
	id := ids[0]

	// 1) 非规范 cwd 且目标会话真实存在：必须拒绝
	payload, _ := json.Marshal(map[string]any{"sessionUpdate": "session_info_update", "title": "hacked"})
	forged := cwd2SessionID(dir+string(filepath.Separator)+".", id)
	if _, err := HandleSessionUpdate(SessionUpdateRequest{SessionID: forged, Update: payload}, nil, 1); err == nil {
		t.Error("非规范 cwd 的 session/update 必须拒绝")
	}
	var reloaded structs.Chats
	if err := db.First(&reloaded, id).Error; err != nil {
		t.Fatalf("reload chat: %v", err)
	}
	if reloaded.Title == "hacked" {
		t.Error("伪造 cwd 的 session/update 不得改到真实会话")
	}

	// 2) 未初始化 .alkaid0 的目录：不得创建会话库
	victim := t.TempDir()
	sid2 := cwd2SessionID(victim, id)
	if _, err := HandleSessionUpdate(SessionUpdateRequest{SessionID: sid2, Update: payload}, nil, 1); err == nil {
		t.Error("未知 cwd 的 session/update 必须报错")
	}
	if _, statErr := os.Stat(filepath.Join(victim, ".alkaid0")); !os.IsNotExist(statErr) {
		t.Error("session/update 不得凭 sessionId 在任意目录创建 .alkaid0 会话库")
	}
	_ = dir
}

// ---- A12: prompt/cancel/close 必须校验连接与会话的绑定 ----

func TestSessionOpsRequireConnectionBinding(t *testing.T) {
	_, _, sessionID := newResumedSession(t, 1)

	// 未绑定会话的连接（999）不得操作注册表中的会话
	if _, err := SessionCancel(SessionCancelRequest{SessionID: sessionID}, nil, 999); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Errorf("未绑定连接不得 cancel: %v", err)
	}
	if _, err := SessionPrompt(SessionPromptRequest{
		SessionID: sessionID,
		Prompt:    []u.H{{"type": "text", "text": "/version"}},
	}, nil, 999); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Errorf("未绑定连接不得 prompt: %v", err)
	}
	if _, err := SessionClose(SessionCloseRequest{SessionID: sessionID}, nil, 999); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Errorf("未绑定连接不得 close: %v", err)
	}
	if _, ok := lookupSession(sessionID); !ok {
		t.Error("未绑定连接的调用不得影响会话注册表")
	}

	// 已通过 session/resume 绑定的连接（1）可正常操作
	if _, err := SessionPrompt(SessionPromptRequest{
		SessionID: sessionID,
		Prompt:    []u.H{{"type": "text", "text": "/version"}},
	}, nil, 1); err != nil {
		t.Errorf("已绑定连接应可 prompt: %v", err)
	}
	if _, err := SessionCancel(SessionCancelRequest{SessionID: sessionID}, nil, 1); err != nil {
		t.Errorf("已绑定连接应可 cancel: %v", err)
	}
	closeSession(sessionID)
}
