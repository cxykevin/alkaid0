package request

// phase 字段（GPT/Codex 系 API 的 commentary / final_answer）落库路径的流式测试。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// phaseTestSession 建立会话（含一条 user 消息）并返回 SendRequest 所需的会话对象。
func phaseTestSession(t *testing.T, db *gorm.DB, chatID uint32) *storageStructs.Chats {
	t.Helper()
	if err := db.Create(&storageStructs.Chats{ID: chatID, LastModelID: 1}).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{
		ChatID: chatID, Type: storageStructs.MessagesRoleUser, Delta: "go ahead",
	}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	return &storageStructs.Chats{
		ID: chatID, DB: db, LastModelID: 1, InTestFlag: true,
		EnableScopes: make(map[string]bool),
	}
}

// phaseAssistantMessage 取出该会话唯一的 assistant 消息。
func phaseAssistantMessage(t *testing.T, db *gorm.DB, chatID uint32) storageStructs.Messages {
	t.Helper()
	var msgs []storageStructs.Messages
	if err := db.Where("chat_id = ? AND type = ?", chatID, storageStructs.MessagesRoleAgent).Find(&msgs).Error; err != nil {
		t.Fatalf("query agent msgs: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected one assistant message, got %d", len(msgs))
	}
	return msgs[0]
}

// TestSendRequestPersistsPhaseLastValueWins 一轮里先 commentary（叙述）后 final_answer（收尾）时，
// 落库的是最后一个非空 phase，且正文完整保留（回放时按模型开关原样回传给模型）。
func TestSendRequestPersistsPhaseLastValueWins(t *testing.T) {
	initAgentsConsumer()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-phase", Model: "native-e2e",
			Choices: []structs.Choice{{Index: 0, Delta: structs.Message{
				Role: structs.RoleAssistant, Content: "let me check", Phase: "commentary",
			}}},
		})
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-phase", Model: "native-e2e",
			Choices: []structs.Choice{{Index: 0, Delta: structs.Message{
				Content: "all done", Phase: "final_answer",
			}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	session := phaseTestSession(t, db, 9101)

	if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	msg := phaseAssistantMessage(t, db, session.ID)
	if msg.Phase != "final_answer" {
		t.Errorf("phase 应取本轮最后一次非空值 final_answer，got %q", msg.Phase)
	}
	if !strings.Contains(msg.Delta, "let me check") || !strings.Contains(msg.Delta, "all done") {
		t.Errorf("两段正文都应落库，got %q", msg.Delta)
	}
}

// TestSendRequestPersistsPhaseAfterFlush 末片的增量只有 phase（无正文）时也要落库：
// 前一段正文已达刷写阈值（写入 commentary + 记录 lastFlushPhase），
// 末片只翻转 phase 到 final_answer——正文长度未变，必须靠 phase 变化触发收尾更新，
// 否则库里会停在过期的 commentary 上。
func TestSendRequestPersistsPhaseAfterFlush(t *testing.T) {
	initAgentsConsumer()
	longText := strings.Repeat("x", 300) // 超过刷写阈值（256 字符），触发中途落库
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-phase-flush", Model: "native-e2e",
			Choices: []structs.Choice{{Index: 0, Delta: structs.Message{
				Role: structs.RoleAssistant, Content: longText, Phase: "commentary",
			}}},
		})
		// 末片：只有 phase，没有正文增量
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-phase-flush", Model: "native-e2e",
			Choices: []structs.Choice{{Index: 0, Delta: structs.Message{Phase: "final_answer"}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	session := phaseTestSession(t, db, 9102)

	if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	msg := phaseAssistantMessage(t, db, session.ID)
	if msg.Phase != "final_answer" {
		t.Errorf("末片的 phase 必须落库，got %q", msg.Phase)
	}
	if len(msg.Delta) != len(longText) {
		t.Errorf("正文不应被改动：期望 %d 字符，got %d", len(longText), len(msg.Delta))
	}
}
