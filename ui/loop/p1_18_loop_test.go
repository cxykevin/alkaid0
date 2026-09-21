package loop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
)

// === P1-18 / F1：SetCallback 死锁、重复消费者、可 join ===

// TestP118SetCallbackPanicDoesNotKillConsumer 回调 panic 后消费 goroutine 必须继续
// 工作并释放 recvSyncQueue 令牌；修复前 panic 会杀死 goroutine，call() 的发送方
// 永远阻塞在 <-recvSyncQueue（Start 正阻塞在 call() 中）→ 真死锁。
func TestP118SetCallbackPanicDoesNotKillConsumer(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)
	loopObj := New(chat)

	var processed atomic.Int64
	loopObj.SetCallback(func(resp AIResponse) {
		if resp.Content == "boom" {
			panic("callback panic (expected in this test)")
		}
		processed.Add(1)
	})

	// 第一条：触发 panic
	loopObj.recvQueue <- AIResponse{Content: "boom"}
	select {
	case <-loopObj.recvSyncQueue:
	case <-time.After(3 * time.Second):
		t.Fatal("callback panic 后未释放 recvSyncQueue 令牌：消费 goroutine 已死")
	}

	// 第二条：panic 之后消费者必须仍然存活
	loopObj.recvQueue <- AIResponse{Content: "ok"}
	select {
	case <-loopObj.recvSyncQueue:
	case <-time.After(3 * time.Second):
		t.Fatal("callback panic 后消费者未继续处理后续响应")
	}
	if got := processed.Load(); got != 1 {
		t.Fatalf("processed=%d, want 1", got)
	}
}

// TestP118SetCallbackRejectsDuplicateRegistration 重复注册必须被拒绝：修复前会启动
// 第二个消费者，并发处理同一轮响应（破坏一消息一令牌配对与顺序假设）。
func TestP118SetCallbackRejectsDuplicateRegistration(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)
	loopObj := New(chat)

	entered := make(chan struct{})
	release := make(chan struct{})
	var firstOnce sync.Once
	loopObj.SetCallback(func(resp AIResponse) {
		firstOnce.Do(func() { close(entered) })
		<-release
	})
	var secondCalls atomic.Int64
	loopObj.SetCallback(func(resp AIResponse) { secondCalls.Add(1) })

	loopObj.recvQueue <- AIResponse{Content: "first"}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first consumer did not start")
	}
	// 第一个消费者被回调阻塞；若存在第二个消费者，它会取走这条消息
	loopObj.recvQueue <- AIResponse{Content: "second"}
	time.Sleep(300 * time.Millisecond)
	if got := secondCalls.Load(); got != 0 {
		t.Fatalf("duplicate SetCallback registered a second consumer (calls=%d)", got)
	}
	close(release)
}

// === P1-18 / F2：审批身份绑定 ===

// TestP118ApproveCarriesMessageID Approve() 必须把当前待审批消息 ID 随命令入队；
// 修复前 msgActionApprove 不携带任何身份，执行端只能猜"最新一条工具调用消息"。
func TestP118ApproveCarriesMessageID(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)
	chat.CurrentMessageID = 4242
	loopObj := New(chat)

	if err := loopObj.Approve(); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	select {
	case msg := <-loopObj.sendQueue:
		if msg.Command != msgActionApprove {
			t.Fatalf("command=%v, want msgActionApprove", msg.Command)
		}
		if msg.MsgID != 4242 {
			t.Fatalf("approve command carried MsgID=%d, want 4242（审批身份丢失）", msg.MsgID)
		}
	case <-time.After(time.Second):
		t.Fatal("approve command not queued")
	}
}

// TestP118StaleApproveDoesNotExecuteNorStartRequest 旧批准（消息 42）在新待审批消息
// （77）出现后才被执行时，必须被拒绝：不得执行任何工具、不得开启新一轮请求、
// 不得把 NewMessageID 改写成别的东西。
func TestP118StaleApproveDoesNotExecuteNorStartRequest(t *testing.T) {
	setupConfigForTest()
	config.GlobalConfig.Model.Models[1] = structs.ModelConfig{
		ModelName: "unreachable", ModelID: "unreachable",
		ProviderURL: "http://127.0.0.1:1", ProviderKey: "k",
	}
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)
	chat.State = state.StateWaitApprove
	chat.CurrentMessageID = 77
	// 会话以 WaitApprove 恢复但没有待审批消息：Start 的启动恢复分支会发一个
	// StopReasonModel 回调后进入等待，不影响下面的审批断言。
	loopObj := New(chat)

	responses := make(chan AIResponse, 32)
	loopObj.SetCallback(func(resp AIResponse) {
		select {
		case responses <- resp:
		default:
		}
	})
	go loopObj.Start(context.Background())
	defer loopObj.Cancel()
	time.Sleep(300 * time.Millisecond)

	// 用户点击的是旧对话框（消息 42），此时待审批消息已被替换为 77
	loopObj.sendQueue <- msgObj{Command: msgActionApprove, MsgID: 42}

	deadline := time.After(2 * time.Second)
	sawStaleRejection := false
drain:
	for {
		select {
		case resp := <-responses:
			if resp.StopReason == StopReasonUser && resp.Error != nil &&
				strings.Contains(resp.Error.Error(), "pending tool call changed") {
				sawStaleRejection = true
			}
		case <-deadline:
			break drain
		}
	}
	if !sawStaleRejection {
		t.Fatal("旧批准未被拒绝（未收到 pending tool call changed 的 StopReasonUser 回调）")
	}

	// 多等一段时间：若旧批准错误地开启了新一轮请求，这里会留下占位消息
	time.Sleep(300 * time.Millisecond)
	var count int64
	if err := db.Model(&storageStructs.Messages{}).Where("chat_id = ?", chat.ID).Count(&count).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 0 {
		t.Fatalf("旧批准开启了新一轮请求：产生了 %d 条消息", count)
	}
	if chat.CurrentMessageID != 77 {
		t.Fatalf("CurrentMessageID=%d, want 77（旧批准改写了待审批身份）", chat.CurrentMessageID)
	}
	if chat.State != state.StateWaitApprove {
		t.Fatalf("State=%v, want WaitApprove（新消息的审批仍在等待用户）", chat.State)
	}
}

// === P1-18 / F5：摘要必须可被 Stop()/ESC 取消 ===

// TestP118StopCancelsSummary Stop() 必须取消进行中的摘要请求。
// 修复前摘要只受 120s SummaryTimeout 限制：ESC 后主循环最长要等 2 分钟。
func TestP118StopCancelsSummary(t *testing.T) {
	setupConfigForTest()

	started := make(chan struct{})
	var startOnce sync.Once
	release := make(chan struct{})
	var releaseOnce sync.Once
	canceled := make(chan struct{})
	var cancelOnce sync.Once
	var handlerWG sync.WaitGroup
	handlerWG.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		// 必须先读完请求体：否则 Go http server 不会做后台读，
		// 客户端断开连接时 r.Context() 不会触发，无法观测取消。
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
			cancelOnce.Do(func() { close(canceled) })
		case <-release:
		}
		handlerWG.Done()
	}))
	defer func() {
		releaseOnce.Do(func() { close(release) })
		handlerWG.Wait()
		srv.Close()
	}()

	config.GlobalConfig.Agent.SummaryModel = 1
	config.GlobalConfig.Model.Models[1] = structs.ModelConfig{
		ModelName: "summary-model", ModelID: "summary-model",
		ProviderURL: srv.URL, ProviderKey: "k",
	}

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)
	// build.Summary 需要 > summaryKeepNumber(6) 条消息才会产生非 0 的 lastMsgID
	for i := range 8 {
		if err := db.Create(&storageStructs.Messages{
			ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "消息" + string(rune('A'+i)),
		}).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}

	loopObj := New(chat)
	loopObj.SetCallback(func(resp AIResponse) {})
	go loopObj.Start(context.Background())
	defer loopObj.Cancel()
	time.Sleep(150 * time.Millisecond)

	if err := loopObj.Summary(); err != nil {
		t.Fatalf("Summary: %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("摘要请求未发出")
	}

	loopObj.Stop()
	stoppedSummary := false
	select {
	case <-canceled:
		stoppedSummary = true
	case <-time.After(2 * time.Second):
	}
	if !stoppedSummary {
		t.Fatal("Stop() 未取消摘要请求：SummarySession 仍被 120s 超时卡住")
	}
}
