package actions

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openMsgQueueTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&structs.MsgQueueItems{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestMsgQueueDispatchFIFO 验证一次唤醒只派发一条、按 FIFO 顺序出队。
func TestMsgQueueDispatchFIFO(t *testing.T) {
	dispatched := make(chan string, 8)
	q := newMsgQueue(1, nil, func() bool { return true }, func(it *msgQueueItem) error {
		dispatched <- it.Text
		return nil
	}, nil)
	q.start()
	defer q.stop()

	for _, text := range []string{"a", "b", "c"} {
		if _, err := q.enqueue(msgQueueKindPrompt, "", "", text); err != nil {
			t.Fatalf("enqueue %q: %v", text, err)
		}
	}
	for _, want := range []string{"a", "b", "c"} {
		q.wakeup()
		select {
		case got := <-dispatched:
			if got != want {
				t.Fatalf("dispatch order: got %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	if _, items := q.snapshot(); len(items) != 0 {
		t.Fatalf("queue should be empty, got %d item(s)", len(items))
	}
}

// TestMsgQueueWaitsForIdle 验证非 Idle 状态不会出队。
func TestMsgQueueWaitsForIdle(t *testing.T) {
	var idle atomic.Bool
	dispatched := make(chan string, 4)
	q := newMsgQueue(2, nil, func() bool { return idle.Load() }, func(it *msgQueueItem) error {
		dispatched <- it.Text
		return nil
	}, nil)
	q.start()
	defer q.stop()

	if _, err := q.enqueue(msgQueueKindPrompt, "", "", "hello"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	q.wakeup()
	select {
	case got := <-dispatched:
		t.Fatalf("dispatched %q while session is not idle", got)
	case <-time.After(100 * time.Millisecond):
	}

	idle.Store(true)
	q.wakeup()
	select {
	case got := <-dispatched:
		if got != "hello" {
			t.Fatalf("got %q, want hello", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch after idle")
	}
}

// TestMsgQueueCapacityAndMutations 验证 32 上限、修改、取消与清空。
func TestMsgQueueCapacityAndMutations(t *testing.T) {
	q := newMsgQueue(3, nil, func() bool { return false }, nil, nil)
	for i := range msgQueueCapacity {
		if _, err := q.enqueue(msgQueueKindPrompt, "", "", fmt.Sprintf("m%d", i)); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if _, err := q.enqueue(msgQueueKindPrompt, "", "", "overflow"); err == nil {
		t.Fatal("expected queue full error")
	}
	if _, items := q.snapshot(); len(items) != msgQueueCapacity {
		t.Fatalf("queue length = %d, want %d", len(items), msgQueueCapacity)
	}

	edited := "edited"
	items, _, err := q.update("mq_3_1", &edited, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if items[0].Text != "edited" {
		t.Fatalf("updated text = %q", items[0].Text)
	}

	items, _, err = q.remove("mq_3_2")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(items) != msgQueueCapacity-1 {
		t.Fatalf("after remove length = %d", len(items))
	}

	items, _, err = q.clear()
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("after clear length = %d", len(items))
	}
}

// TestMsgQueueUpdateCommandArgs command 项只改 args，并重建 text。
func TestMsgQueueUpdateCommandArgs(t *testing.T) {
	q := newMsgQueue(4, nil, func() bool { return false }, nil, nil)
	item, err := q.enqueue(msgQueueKindCommand, "/s", "old", "/s old")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	args := "new phrase"
	items, _, err := q.update(item.ID, nil, &args)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if items[0].Args != args || items[0].Text != "/s new phrase" {
		t.Fatalf("unexpected command item: %+v", items[0])
	}
	if _, _, err := q.update(item.ID, nil, nil); err == nil {
		t.Fatal("expected error when args is missing for command item")
	}
}

// TestMsgQueuePersistsAndReloads 验证队列独立落库、重新加载后顺序不变。
func TestMsgQueuePersistsAndReloads(t *testing.T) {
	db := openMsgQueueTestDB(t)
	q := newMsgQueue(7, db, func() bool { return false }, nil, nil)
	if _, err := q.enqueue(msgQueueKindPrompt, "", "", "hello"); err != nil {
		t.Fatalf("enqueue prompt: %v", err)
	}
	if _, err := q.enqueue(msgQueueKindCommand, "/compress", "", "/compress"); err != nil {
		t.Fatalf("enqueue command: %v", err)
	}

	q2 := newMsgQueue(7, db, func() bool { return false }, nil, nil)
	if err := q2.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	_, items := q2.snapshot()
	if len(items) != 2 || items[0].Text != "hello" || items[1].Kind != msgQueueKindCommand {
		t.Fatalf("unexpected reloaded items: %+v", items)
	}

	if _, _, err := q2.remove(items[0].ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	q3 := newMsgQueue(7, db, func() bool { return false }, nil, nil)
	if err := q3.load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	_, items3 := q3.snapshot()
	if len(items3) != 1 || items3[0].Command != "/compress" {
		t.Fatalf("unexpected items after remove: %+v", items3)
	}
}

// TestMsgQueueBroadcastCapabilityFilter 只有显式声明能力的连接能收到推送。
func TestMsgQueueBroadcastCapabilityFilter(t *testing.T) {
	const sid = "sess_900:/tmp/msgqueue-test"
	const connAllowed, connMissing, connFalse = uint64(9001), uint64(9002), uint64(9003)

	sessionConnLock.Lock()
	sessionConnMap[sid] = []uint64{connAllowed, connMissing, connFalse}
	sessionConnLock.Unlock()
	defer func() {
		sessionConnLock.Lock()
		delete(sessionConnMap, sid)
		sessionConnLock.Unlock()
	}()

	got := make(chan uint64, 4)
	connCallLock.Lock()
	for _, cid := range []uint64{connAllowed, connMissing, connFalse} {
		id := cid
		connCallMap[id] = func(string, any, *string) error {
			got <- id
			return nil
		}
	}
	connCallLock.Unlock()
	defer func() {
		connCallLock.Lock()
		for _, cid := range []uint64{connAllowed, connMissing, connFalse} {
			delete(connCallMap, cid)
		}
		connCallLock.Unlock()
	}()

	clientConnCapsMu.Lock()
	clientConnCaps[connAllowed] = u.H{msgQueueCapability: map[string]any{}}
	clientConnCaps[connMissing] = u.H{}
	clientConnCaps[connFalse] = u.H{msgQueueCapability: false}
	clientConnCapsMu.Unlock()
	defer func() {
		clientConnCapsMu.Lock()
		for _, cid := range []uint64{connAllowed, connMissing, connFalse} {
			delete(clientConnCaps, cid)
		}
		clientConnCapsMu.Unlock()
	}()

	broadcastMsgQueueEvent(sid, msgQueueEvent{
		SessionUpdate: msgQueueUpdateName,
		Revision:      1,
		Reason:        msgQueueReasonEnqueued,
		Items:         []msgQueueItemView{},
	})

	select {
	case id := <-got:
		if id != connAllowed {
			t.Fatalf("push went to conn %d, want %d", id, connAllowed)
		}
	case <-time.After(time.Second):
		t.Fatal("capable connection did not receive queue event")
	}
	select {
	case id := <-got:
		t.Fatalf("unexpected push to conn %d", id)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestConnDeclaresMsgQueue(t *testing.T) {
	cases := []struct {
		name string
		caps u.H
		want bool
	}{
		{"object", u.H{msgQueueCapability: map[string]any{}}, true},
		{"true", u.H{msgQueueCapability: true}, true},
		{"false", u.H{msgQueueCapability: false}, false},
		{"missing", u.H{}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := connDeclaresMsgQueue(tc.caps); got != tc.want {
				t.Fatalf("connDeclaresMsgQueue() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPlanMsgQueueFlush 连续 prompt 合并，command 作为批次分界。
func TestPlanMsgQueueFlush(t *testing.T) {
	items := []msgQueueItem{
		{Kind: msgQueueKindPrompt, Text: "a"},
		{Kind: msgQueueKindPrompt, Text: "b"},
		{Kind: msgQueueKindCommand, Command: "/compress", Text: "/compress"},
		{Kind: msgQueueKindPrompt, Text: "c"},
	}
	actions := planMsgQueueFlush(items)
	if len(actions) != 3 {
		t.Fatalf("actions = %d, want 3", len(actions))
	}
	if actions[0].Command != nil || actions[0].Text != "a\n\nb" {
		t.Fatalf("first action = %+v", actions[0])
	}
	if actions[1].Command == nil || actions[1].Command.Command != "/compress" {
		t.Fatalf("second action = %+v", actions[1])
	}
	if actions[2].Command != nil || actions[2].Text != "c" {
		t.Fatalf("third action = %+v", actions[2])
	}
}

// TestMsgQueueResumesAfterAbnormalStop 复现「服务端异常停止后重启会话，消息队列卡死、
// 不再自动投递下一条」：会话状态是持久化列，异常退出时可能停在 ToolCalling /
// WaitApprove；重启后冷加载若沿用这个陈旧状态，msgQueue 的 worker 永远等不到 Idle，
// 队列里的消息再也不会被投递。
//
// 场景构造：排队一条消息（此时状态非空闲，worker 不会出队）→ 释放会话（等价于进程
// 消失：不改持久化状态、不清队列）→ 冷加载（等价于重启后 resume）。冷加载必须把陈旧
// 状态复位为 Idle，并自动把队列里的消息投递出去。
func TestMsgQueueResumesAfterAbnormalStop(t *testing.T) {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") != "true" {
		t.Skip("ALKAID0_DEBUG_MOCKSERVER not set, skipping test")
	}
	openai.StartServerTask()
	setupConfigForTest()
	// 队列投递走 UserAddMsgWithID 的预处理/分类器路径；关闭预处理便于按原文断言。
	config.GlobalConfig.Agent.DisablePromptPreprocess = true

	// 清理全局状态，避免与其他测试互相影响
	sessions = map[string]*sessionObj{}
	sessLock = &sync.Mutex{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	connCallLock = &sync.Mutex{}
	sessionConnMap = map[string][]uint64{}
	sessionConnLock = &sync.Mutex{}
	bindedSessionOnConn = map[uint64][]string{}
	bindedSessionOnConnMu = &sync.Mutex{}

	const connID = uint64(9301)
	clientConnCapsMu.Lock()
	clientConnCaps[connID] = u.H{msgQueueCapability: map[string]any{}}
	clientConnCapsMu.Unlock()
	t.Cleanup(func() {
		clientConnCapsMu.Lock()
		delete(clientConnCaps, connID)
		clientConnCapsMu.Unlock()
	})

	tmpDir, err := os.MkdirTemp("", "alkaid0_msgqueue_restart_test_")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	call := func(string, any, *string) error { return nil }
	resp, err := SessionNew(SessionNewRequest{Cwd: tmpDir}, call, connID)
	if err != nil {
		t.Fatalf("SessionNew: %v", err)
	}
	sessionID := resp.SessionID
	cwd, chatID, err := sessionID2Cwd(sessionID)
	if err != nil {
		t.Fatalf("sessionID2Cwd: %v", err)
	}
	obj, ok := lookupSession(sessionID)
	if !ok {
		t.Fatal("session should be registered after SessionNew")
	}

	// 崩溃前：回合停在工具执行中（该状态会落库），队列里还有一条待投递消息。
	if err := obj.session.SaveState(state.StateToolCalling); err != nil {
		t.Fatalf("simulate stale state: %v", err)
	}
	queued := "queued message after abnormal stop"
	if _, err := obj.msgQueue.enqueue(msgQueueKindPrompt, "", "", queued); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// 模拟进程消失：正常释放会停掉 loop 与队列 worker，但不会改写持久化的会话状态，
	// 也不会清空队列表，因此冷加载看到的就是崩溃现场。
	closeSession(sessionID)
	if _, ok := lookupSession(sessionID); ok {
		t.Fatal("session should be released to simulate a restarted process")
	}

	// 冷加载（服务端重启后客户端 resume）。
	if _, err := SessionResume(SessionResumeRequest{Cwd: cwd, SessionID: sessionID}, call, connID); err != nil {
		t.Fatalf("SessionResume: %v", err)
	}
	reloaded, ok := lookupSession(sessionID)
	if !ok {
		t.Fatal("session should be registered after cold restore")
	}
	defer closeSession(sessionID)

	// 冷加载复位后队列会立刻开始投递，状态可能已经推进到 Waiting 等，因此不能断言
	// 此刻仍是 Idle。判别标准是「不再是崩溃前落库的 ToolCalling」：复位失败时 worker
	// 永远等不到 Idle，队列不会出队（下面的断言会失败）。
	if got := reloaded.session.GetState(); got == state.StateToolCalling {
		t.Fatalf("cold loaded state = %d: stale state was not reset", got)
	}

	// 队列应自动投递：消息写入主消息库，队列表里的行被删除。
	deadline := time.Now().Add(5 * time.Second)
	for {
		var n int64
		if err := reloaded.session.DB.Model(&structs.Messages{}).
			Where("chat_id = ? AND type = ? AND delta = ?", chatID, structs.MessagesRoleUser, queued).
			Count(&n).Error; err != nil {
			t.Fatalf("query messages: %v", err)
		}
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued message was not dispatched after cold load (message queue stuck)")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 队列行在派发成功后才删除（先落库、再删行，避免丢消息），所以「消息已入库」
	// 与「行已删除」之间存在一个窗口：Windows/macOS（尤其 -race）上足以让一次性
	// 断言偶发失败，这里同样轮询等待。
	queueDeadline := time.Now().Add(5 * time.Second)
	for {
		var left int64
		if err := reloaded.session.DB.Model(&structs.MsgQueueItems{}).
			Where("chat_id = ?", chatID).Count(&left).Error; err != nil {
			t.Fatalf("query queue rows: %v", err)
		}
		if left == 0 {
			break
		}
		if time.Now().After(queueDeadline) {
			t.Fatalf("queue rows left after dispatch = %d, want 0", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMsgQueueStopIdempotent stop 可重复调用且不 panic。
func TestMsgQueueStopIdempotent(t *testing.T) {
	q := newMsgQueue(5, nil, func() bool { return false }, nil, nil)
	q.start()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			q.stop()
		})
	}
	wg.Wait()
}

// TestMsgQueuePromptWithCapability 端到端：声明能力的连接收到队列事件，
// 队列项出队后写入主消息库并广播 user_message。
func TestMsgQueuePromptWithCapability(t *testing.T) {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") != "true" {
		t.Skip("ALKAID0_DEBUG_MOCKSERVER not set, skipping test")
	}
	openai.StartServerTask()
	setupConfigForTest()

	// 清理全局状态，避免与其他测试互相影响
	sessions = map[string]*sessionObj{}
	sessLock = &sync.Mutex{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	connCallLock = &sync.Mutex{}
	sessionConnMap = map[string][]uint64{}
	sessionConnLock = &sync.Mutex{}
	bindedSessionOnConn = map[uint64][]string{}
	bindedSessionOnConnMu = &sync.Mutex{}

	const connID = uint64(9101)
	clientConnCapsMu.Lock()
	clientConnCaps[connID] = u.H{msgQueueCapability: map[string]any{}}
	clientConnCapsMu.Unlock()
	t.Cleanup(func() {
		clientConnCapsMu.Lock()
		delete(clientConnCaps, connID)
		clientConnCapsMu.Unlock()
	})

	// 与既有集成测试一致：用 os.MkdirTemp + 忽略 RemoveAll 错误。
	// Windows 上 codebase 后台索引可能仍持有 codebase.sqlite，t.TempDir 的清理
	// 会把这种“文件被占用”当成测试失败（Linux/macOS 不会）。
	tmpDir, err := os.MkdirTemp("", "alkaid0_msgqueue_test_")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	calls := make(chan ReceivedCall, 500)
	call := func(name string, data any, _ *string) error {
		calls <- ReceivedCall{Name: name, Data: data}
		return nil
	}
	resp, err := SessionNew(SessionNewRequest{Cwd: tmpDir}, call, connID)
	if err != nil {
		t.Fatalf("SessionNew: %v", err)
	}
	if _, err := SessionPrompt(SessionPromptRequest{
		SessionID: resp.SessionID,
		Prompt:    []u.H{{"type": "text", "text": "hello queue"}},
	}, nil, connID); err != nil {
		t.Fatalf("SessionPrompt: %v", err)
	}

	if _, ok := waitForUpdate(calls, func(rc ReceivedCall) bool {
		su, ok := rc.Data.(SessionUpdate)
		if !ok {
			return false
		}
		ev, ok := su.Update.(msgQueueEvent)
		return ok && ev.SessionUpdate == msgQueueUpdateName
	}, 5*time.Second); !ok {
		t.Fatal("did not receive msgqueue_update")
	}

	if _, ok := waitForUpdate(calls, func(rc ReceivedCall) bool {
		su, ok := rc.Data.(SessionUpdate)
		if !ok {
			return false
		}
		upd, ok := su.Update.(SessionUpdateUpdate)
		return ok && upd.SessionUpdate == "user_message"
	}, 5*time.Second); !ok {
		t.Fatal("did not receive user_message after dequeue")
	}

	closeSession(resp.SessionID)
}
