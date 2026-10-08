package loop

import (
	"context"
	"testing"
	"time"

	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// TestRewind 命令入队：携带 msgActionRewind 与目标消息 ID。
func TestRewind(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	if err := loopObj.Rewind(42); err != nil {
		t.Fatalf("Expected no error when calling Rewind: %v", err)
	}

	select {
	case msg := <-loopObj.sendQueue:
		if msg.Command != msgActionRewind {
			t.Fatalf("Expected command msgActionRewind, got %d", msg.Command)
		}
		if msg.MsgID != 42 {
			t.Fatalf("Expected rewind to carry MsgID 42, got %d", msg.MsgID)
		}
	case <-time.After(time.Second):
		t.Fatal("Expected rewind command to be queued")
	}
}

// TestRewindQueueFull 队列满时 Rewind 返回错误而非阻塞。
func TestRewindQueueFull(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	for range queueSize {
		select {
		case loopObj.sendQueue <- msgObj{Msg: "test"}:
		default:
			t.Fatal("unexpected: sendQueue not full yet")
		}
	}

	if err := loopObj.Rewind(1); err == nil {
		t.Fatal("expected error when rewind queue is full")
	}
}

// rewindTestMessages 在会话活跃分支上追加消息，返回消息切片（最后一条为当前会话头）。
func rewindTestMessages(t *testing.T, db *gorm.DB, chatID uint32, deltas ...string) []*storageStructs.Messages {
	t.Helper()
	msgs := make([]*storageStructs.Messages, 0, len(deltas))
	for i, delta := range deltas {
		typ := storageStructs.MessagesRoleUser
		if i%2 == 1 {
			typ = storageStructs.MessagesRoleAgent
		}
		msg := &storageStructs.Messages{ChatID: chatID, Delta: delta, Type: typ}
		if err := storageStructs.AppendMessage(db, msg); err != nil {
			t.Fatalf("append message %q: %v", delta, err)
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

// rewindTestWait 收集 rewind 结果与随后的命令轮收尾响应（StopReason 非 None 的那条）。
func rewindTestWait(t *testing.T, ch <-chan AIResponse) (AIResponse, AIResponse) {
	t.Helper()
	var rewindResp, stopResp *AIResponse
	timeout := time.After(5 * time.Second)
	for rewindResp == nil || stopResp == nil {
		select {
		case resp := <-ch:
			r := resp
			if r.Rewind != nil {
				rewindResp = &r
			}
			if r.StopReason != StopReasonNone {
				stopResp = &r
			}
		case <-timeout:
			t.Fatalf("timeout waiting for rewind responses (rewind=%v stop=%v)", rewindResp != nil, stopResp != nil)
		}
	}
	return *rewindResp, *stopResp
}

// TestRewindLoopSuccess Start 处理 rewind 指令：会话头移到目标消息，
// 回调报告成功，随后按命令轮以 StopReasonUser 收尾；消息全部保留。
func TestRewindLoopSuccess(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	msgs := rewindTestMessages(t, db, chat.ID, "m1", "m2", "m3")
	target := msgs[1].ID

	loopObj := New(chat)
	respChan := make(chan AIResponse, 16)
	loopObj.SetCallback(func(resp AIResponse) { respChan <- resp })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loopObj.Start(ctx)
	}()

	if err := loopObj.Rewind(target); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	rewindResp, stopResp := rewindTestWait(t, respChan)
	cancel()
	<-done
	loopObj.Cancel()

	if rewindResp.Rewind == nil || rewindResp.Rewind.Err != nil {
		t.Fatalf("rewind result = %+v, want success", rewindResp.Rewind)
	}
	if rewindResp.Rewind.MsgID != target {
		t.Fatalf("rewind result MsgID = %d, want %d", rewindResp.Rewind.MsgID, target)
	}
	if stopResp.StopReason != StopReasonUser || stopResp.Error != nil {
		t.Fatalf("expected clean command-round finish, got stop=%v err=%v", stopResp.StopReason, stopResp.Error)
	}

	var fresh storageStructs.Chats
	if err := db.First(&fresh, chat.ID).Error; err != nil {
		t.Fatalf("reload chat: %v", err)
	}
	if fresh.ActiveLeafID == nil || *fresh.ActiveLeafID != target {
		t.Fatalf("ActiveLeafID = %v, want %d", fresh.ActiveLeafID, target)
	}

	// rewind 不删除任何消息：三条消息仍在。
	var count int64
	if err := db.Model(&storageStructs.Messages{}).Where("chat_id = ?", chat.ID).Count(&count).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 3 {
		t.Fatalf("message count = %d, want 3 (rewind must not delete messages)", count)
	}
}

// TestRewindLoopFailure 目标消息不存在：回调报告失败原因，
// 随后按命令轮以 StopReasonError 收尾，且不产生任何数据改动。
func TestRewindLoopFailure(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	msgs := rewindTestMessages(t, db, chat.ID, "m1", "m2")
	leafBefore := msgs[1].ID

	loopObj := New(chat)
	respChan := make(chan AIResponse, 16)
	loopObj.SetCallback(func(resp AIResponse) { respChan <- resp })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loopObj.Start(ctx)
	}()

	if err := loopObj.Rewind(999999); err != nil {
		t.Fatalf("Rewind: %v", err)
	}

	rewindResp, stopResp := rewindTestWait(t, respChan)
	cancel()
	<-done
	loopObj.Cancel()

	if rewindResp.Rewind == nil || rewindResp.Rewind.Err == nil {
		t.Fatalf("rewind result = %+v, want failure", rewindResp.Rewind)
	}
	if rewindResp.Rewind.MsgID != 999999 {
		t.Fatalf("rewind result MsgID = %d, want 999999", rewindResp.Rewind.MsgID)
	}
	if stopResp.StopReason != StopReasonError || stopResp.Error == nil {
		t.Fatalf("expected error command-round finish, got stop=%v err=%v", stopResp.StopReason, stopResp.Error)
	}

	var fresh storageStructs.Chats
	if err := db.First(&fresh, chat.ID).Error; err != nil {
		t.Fatalf("reload chat: %v", err)
	}
	if fresh.ActiveLeafID == nil || *fresh.ActiveLeafID != leafBefore {
		t.Fatalf("ActiveLeafID = %v, want unchanged %d", fresh.ActiveLeafID, leafBefore)
	}
}
