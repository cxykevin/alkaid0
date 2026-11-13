package actions

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/loop"
)

// TestResumeMidStreamShowsInMemorySnapshot 中途加入（或重连）的连接必须看到
// 已经生成的部分，并且这部分来自**纯内存快照**：流式过程中内容列保持为空
// （正文只走 activeAgentMessages 快照 + stream_chunks 追加日志），只有在收尾
// 事务里才写进 messages.delta。
//
// 同时验证不会重复：历史回放会跳过正在生成的消息（activeMessageIDs），
// 该消息只以"整条快照"的形式出现一次。
func TestResumeMidStreamShowsInMemorySnapshot(t *testing.T) {
	dir, chatID, sessionID := newResumedSession(t, 1)
	defer closeSession(sessionID)
	obj, ok := lookupSession(sessionID)
	if !ok {
		t.Fatal("session lost")
	}

	db, err := loadDB(dir)
	if err != nil {
		t.Fatalf("loadDB: %v", err)
	}
	defer closeDB(dir)

	// 与 SendRequest 开始时一致：先建一条空的 assistant 占位行，再开始收 chunk
	msg := &structs.Messages{ChatID: chatID, Type: structs.MessagesRoleAgent}
	if err := structs.AppendMessage(db, msg); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// 流式进行到一半：正文分两片、思考一片
	obj.loopCallback(loop.AIResponse{MsgID: msg.ID, Content: "已经生成"})
	obj.loopCallback(loop.AIResponse{MsgID: msg.ID, ThinkingContext: "想一想", Content: "的部分"})

	// 此刻数据库里没有任何内容：内容列只在收尾时写一次
	var persisted structs.Messages
	if err := db.First(&persisted, msg.ID).Error; err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	if persisted.Delta != "" || persisted.ThinkingDelta != "" {
		t.Fatalf("流式过程中内容列应为空（内容走内存快照与追加日志），得到 delta=%q thinking=%q",
			persisted.Delta, persisted.ThinkingDelta)
	}

	// 第二个连接中途 resume，并要求回放历史
	type event struct{ kind, message, text string }
	var mu sync.Mutex
	var events []event
	call := func(method string, params any, _ *string) error {
		if method != "session/update" || params == nil {
			return nil
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return nil
		}
		var payload struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				MessageID     string `json:"messageId"`
				Content       []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil
		}
		var text string
		for _, c := range payload.Update.Content {
			text += c.Text
		}
		mu.Lock()
		events = append(events, event{payload.Update.SessionUpdate, payload.Update.MessageID, text})
		mu.Unlock()
		return nil
	}
	if _, err := SessionResume(SessionResumeRequest{
		Cwd:        dir,
		SessionID:  sessionID,
		ReplayFrom: &ReplayFrom{Type: "start"},
	}, call, 2); err != nil {
		t.Fatalf("SessionResume: %v", err)
	}

	wantID := msgID(msg.ID)
	snapshots, thoughts := 0, 0
	for _, e := range events {
		if e.message != wantID {
			continue
		}
		switch e.kind {
		case "agent_message":
			snapshots++
			if e.text != "已经生成的部分" {
				t.Errorf("内存快照正文不完整：%q", e.text)
			}
		case "agent_thought":
			thoughts++
			if e.text != "想一想" {
				t.Errorf("内存快照思考不完整：%q", e.text)
			}
		}
	}
	if snapshots != 1 {
		t.Errorf("中途加入应恰好收到一次完整正文快照（回放跳过同一消息以免重复），got %d", snapshots)
	}
	if thoughts != 1 {
		t.Errorf("中途加入应恰好收到一次完整思考快照，got %d", thoughts)
	}
}
