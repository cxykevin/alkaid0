package loop

import (
	"sync/atomic"
	"testing"
	"time"

	u "github.com/cxykevin/alkaid0/utils"
)

// TestP118WaitCallbackJoinsConsumer Cancel 后回调 goroutine 必须可 join，
// 以便会话释放在 closeDB 之前确认它已退出（A10 残留）。
func TestP118WaitCallbackJoinsConsumer(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)
	loopObj := New(chat)

	var calls atomic.Int64
	loopObj.SetCallback(func(resp AIResponse) { calls.Add(1) })
	loopObj.recvQueue <- AIResponse{Content: "x"}
	select {
	case <-loopObj.recvSyncQueue:
	case <-time.After(3 * time.Second):
		t.Fatal("callback did not run")
	}

	loopObj.Cancel() // 关闭 done → 消费 goroutine 退出
	if !loopObj.WaitCallback(3 * time.Second) {
		t.Fatal("WaitCallback: 回调 goroutine 未在超时内退出")
	}
	// 未注册回调时也必须立即返回 true（不阻塞关库路径）
	empty := New(chat)
	if !empty.WaitCallback(100 * time.Millisecond) {
		t.Fatal("WaitCallback should return immediately when no callback registered")
	}
}
