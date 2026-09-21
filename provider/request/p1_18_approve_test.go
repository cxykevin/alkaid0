package request

import (
	"errors"
	"testing"

	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
)

// === P1-18 / F2：审批身份绑定 ===

// TestP118ApproveToolCallsByIDRejectsStaleMessage 用户批准的消息已不是当前待审批
// 消息时，不得执行任何工具，且会话必须保持等待（新消息的审批仍在等用户）。
func TestP118ApproveToolCallsByIDRejectsStaleMessage(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := storageStructs.Chats{ID: 9200}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	msg1 := storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleAgent,
		ToolCallingJSONString: `[{"name":"read","id":"c1","parameters":{"path":"a.txt"}}]`}
	if err := db.Create(&msg1).Error; err != nil {
		t.Fatalf("create msg1: %v", err)
	}
	msg2 := storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleAgent,
		ToolCallingJSONString: `[{"name":"read","id":"c2","parameters":{"path":"b.txt"}}]`}
	if err := db.Create(&msg2).Error; err != nil {
		t.Fatalf("create msg2: %v", err)
	}

	session := &storageStructs.Chats{ID: chat.ID, DB: db, State: state.StateWaitApprove}
	id, err := ApproveToolCallsByID(session, msg1.ID) // 用户批准的是旧消息
	if !errors.Is(err, ErrPendingToolCallChanged) {
		t.Fatalf("stale approve must be rejected, got id=%d err=%v", id, err)
	}
	if session.State != state.StateWaitApprove {
		t.Fatalf("stale approve changed state to %v, want WaitApprove", session.State)
	}
	latest, err := PendingToolCallMessageID(session)
	if err != nil {
		t.Fatalf("PendingToolCallMessageID: %v", err)
	}
	if latest != msg2.ID {
		t.Fatalf("pending message = %d, want %d", latest, msg2.ID)
	}
}

// TestP118ApproveToolCallsByIDNoPendingMessage 已无待审批消息时：
//   - 携带具体身份（msgID != 0）→ 拒绝（该批准已无法满足）；
//   - 未携带身份（msgID == 0，旧调用方）→ 静默 no-op，保持原有语义。
func TestP118ApproveToolCallsByIDNoPendingMessage(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := storageStructs.Chats{ID: 9201}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	session := &storageStructs.Chats{ID: chat.ID, DB: db, State: state.StateWaitApprove}
	id, err := ApproveToolCallsByID(session, 12345)
	if !errors.Is(err, ErrPendingToolCallChanged) {
		t.Fatalf("approve for vanished message must be rejected, got id=%d err=%v", id, err)
	}
	if id != 0 {
		t.Fatalf("expected no executed message, got %d", id)
	}
	id, err = ApproveToolCallsByID(session, 0)
	if err != nil {
		t.Fatalf("identity-less approve should be a no-op, got %v", err)
	}
	if id != 0 {
		t.Fatalf("expected no executed message, got %d", id)
	}
}
