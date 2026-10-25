package actions

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	configStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/mask"
	"github.com/cxykevin/alkaid0/storage"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

func newClientMaskFixture(t *testing.T) *mask.ClientMasker {
	t.Helper()
	restore := config.GlobalConfigSwap(configStructs.Config{
		DataMask: configStructs.DataMaskConfig{
			Enable:             true,
			MaskAPIKey:         true,
			MaskPhone:          true,
			MaskIP:             true,
			MaskJWT:            true,
			MaskClientMessages: true,
		},
	})
	t.Cleanup(restore)

	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	cm := mask.NewClientMasker(db)
	if cm == nil {
		t.Fatal("client masker should be enabled")
	}
	return cm
}

// TestMaskClientPayloadMasksContentOnly 只替换对话内容字段，会话/消息/工具
// 调用 ID 必须原样保留，否则客户端无法关联更新。
func TestMaskClientPayloadMasksContentOnly(t *testing.T) {
	cm := newClientMaskFixture(t)
	payload := u.H{
		"sessionId": "sess_1:/tmp/13800138000",
		"update": u.H{
			"sessionUpdate": "agent_message",
			"messageId":     "msg_7",
			"toolCallId":    "call_1_2_edit",
			"content": []u.H{{
				"type": "text",
				"text": "call me at 13800138000",
			}},
		},
	}

	got, ok := maskClientPayload(cm, payload).(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type %T", maskClientPayload(cm, payload))
	}
	if got["sessionId"] != "sess_1:/tmp/13800138000" {
		t.Fatalf("sessionId changed: %v", got["sessionId"])
	}
	update, ok := got["update"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected update type %T", got["update"])
	}
	if update["messageId"] != "msg_7" || update["toolCallId"] != "call_1_2_edit" {
		t.Fatalf("structural ids changed: %#v", update)
	}
	content, ok := update["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("unexpected content: %#v", update["content"])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if strings.Contains(text, "13800138000") {
		t.Fatalf("content still contains sensitive value: %q", text)
	}
	if text == "call me at 13800138000" {
		t.Fatalf("content was not masked")
	}
}

// TestMaskClientPayloadMasksToolArgs 工具参数（content 下的 args）同样脱敏。
func TestMaskClientPayloadMasksToolArgs(t *testing.T) {
	cm := newClientMaskFixture(t)
	payload := u.H{
		"update": u.H{
			"sessionUpdate": "tool_call_update",
			"content": []u.H{{
				"type": "alk.cxykevin.top/calling_info",
				"args": map[string]any{
					"path":  "main.go",
					"token": "sk-or-v1-abc123def456ghi789",
				},
			}},
		},
	}

	got := maskClientPayload(cm, payload).(map[string]any)
	update := got["update"].(map[string]any)
	block := update["content"].([]any)[0].(map[string]any)
	args := block["args"].(map[string]any)
	if args["token"] == "sk-or-v1-abc123def456ghi789" {
		t.Fatalf("tool args token was not masked: %#v", args)
	}
	if args["path"] != "main.go" {
		t.Fatalf("non-sensitive arg changed: %#v", args)
	}
}

// TestMaskClientPayloadKeepsLargeIDs JSON 往返不能损失 uint64 精度。
func TestMaskClientPayloadKeepsLargeIDs(t *testing.T) {
	cm := newClientMaskFixture(t)
	payload := u.H{
		"update": u.H{
			"sessionUpdate": "usage_update",
			"messageID":     uint64(math.MaxUint64),
			"content":       "no secret",
		},
	}

	got := maskClientPayload(cm, payload)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal masked payload: %v", err)
	}
	if !strings.Contains(string(raw), "18446744073709551615") {
		t.Fatalf("uint64 id lost precision: %s", raw)
	}
}

// TestMaskClientPayloadSkipsFakeValues 已经脱敏过的内容不会二次脱敏
// （活动流快照、重连补发等路径会重复经过展示层）。
func TestMaskClientPayloadSkipsFakeValues(t *testing.T) {
	cm := newClientMaskFixture(t)
	fake := cm.Text("call 13800138000")
	payload := u.H{"update": u.H{"content": fake}}

	got := maskClientPayload(cm, payload).(map[string]any)
	update := got["update"].(map[string]any)
	if update["content"] != fake {
		t.Fatalf("fake value was masked again: %q -> %q", fake, update["content"])
	}
}

// TestBroadcastSessionUpdateMasksClientContent 广播路径（用户消息、回放、
// 工具调用等）在开启配置后发给客户端的内容是脱敏后的。
func TestBroadcastSessionUpdateMasksClientContent(t *testing.T) {
	restore := config.GlobalConfigSwap(configStructs.Config{
		DataMask: configStructs.DataMaskConfig{
			Enable:             true,
			MaskPhone:          true,
			MaskClientMessages: true,
		},
	})
	t.Cleanup(restore)

	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})

	cwd := t.TempDir()
	sessID := registerTestSession(t, cwd, 1)
	sessLock.Lock()
	sessions[sessID].session = &storageStructs.Chats{DB: db}
	sessLock.Unlock()

	var got any
	const connID = uint64(90001)
	registerConnCall(connID, sessID, func(method string, v any, _ *string) error {
		if method == "session/update" {
			got = v
		}
		return nil
	})
	t.Cleanup(func() { unregisterConnCall(connID, sessID) })

	err = broadcastSessionUpdate(sessID, SessionUpdate{
		SessionID: sessID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "user_message",
			MessageID:     "msg_1",
			Content:       []u.H{{"type": "text", "text": "my phone is 13800138000"}},
		},
	}, 0)
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if got == nil {
		t.Fatal("no update was sent to the registered connection")
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal broadcast: %v", err)
	}
	if strings.Contains(string(raw), "13800138000") {
		t.Fatalf("broadcast still contains sensitive value: %s", raw)
	}
	// 结构字段必须保留，客户端才能把更新关联到消息。
	if !strings.Contains(string(raw), "msg_1") {
		t.Fatalf("messageId was lost: %s", raw)
	}
}

// TestIsStreamingClientUpdate 流式 chunk 已由 loop 的流式脱敏器处理，
// 展示层不能再用整段检测覆盖。
func TestIsStreamingClientUpdate(t *testing.T) {
	chunk := SessionUpdate{Update: SessionUpdateUpdate{SessionUpdate: "agent_message_chunk"}}
	if !isStreamingClientUpdate(chunk) {
		t.Fatal("agent_message_chunk should be treated as streaming")
	}
	thought := SessionUpdate{Update: SessionUpdateUpdate{SessionUpdate: "agent_thought_chunk"}}
	if !isStreamingClientUpdate(thought) {
		t.Fatal("agent_thought_chunk should be treated as streaming")
	}
	whole := SessionUpdate{Update: SessionUpdateUpdate{SessionUpdate: "agent_message"}}
	if isStreamingClientUpdate(whole) {
		t.Fatal("agent_message should not be treated as streaming")
	}
	if isStreamingClientUpdate(u.H{"update": u.H{"sessionUpdate": "agent_message_chunk"}}) {
		t.Fatal("unknown update shapes must not be treated as streaming")
	}
}
