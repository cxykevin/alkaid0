package actions

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/ui/loop"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestParseRewindMsgID rewind 参数解析：接受协议形态 msg_<dbID>，也接受裸数字 ID。
func TestParseRewindMsgID(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		want    uint64
		wantErr bool
	}{
		{"protocol messageId", "msg_42", 42, false},
		{"bare db id", "42", 42, false},
		{"trim spaces", "  msg_7 ", 7, false},
		{"empty", "", 0, true},
		{"zero", "msg_0", 0, true},
		{"zero bare", "0", 0, true},
		{"not a number", "abc", 0, true},
		{"negative", "-1", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRewindMsgID(tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseRewindMsgID(%q) expected error, got %d", tt.arg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRewindMsgID(%q) error = %v", tt.arg, err)
			}
			if got != tt.want {
				t.Fatalf("parseRewindMsgID(%q) = %d, want %d", tt.arg, got, tt.want)
			}
		})
	}
}

// TestRewindCommandHidden 内部 rewind 命令注册为隐藏：存在于命令表、
// 不出现在 availableCommandList 中。
func TestRewindCommandHidden(t *testing.T) {
	cmd, ok := commandMaps["/__alk_rewind"]
	if !ok {
		t.Fatal("/__alk_rewind command not registered")
	}
	if !cmd.Hidden {
		t.Error("/__alk_rewind should be Hidden")
	}
	if cmd.Description == "" || cmd.Hint == "" || cmd.Function == nil {
		t.Error("/__alk_rewind should keep complete command fields")
	}

	list := availableCommandList()
	sawHelp := false
	for _, item := range list {
		m, ok := item.(u.H)
		if !ok {
			t.Fatalf("unexpected command entry type %T", item)
		}
		name, _ := m["name"].(string)
		if name == "__alk_rewind" || name == "/__alk_rewind" {
			t.Fatal("hidden command __alk_rewind leaked into availableCommandList")
		}
		if name == "help" || name == "/help" {
			sawHelp = true
		}
	}
	if !sawHelp {
		t.Error("availableCommandList should still contain non-hidden commands (help)")
	}
}

// registerCaptureConn 注册一个捕获 session/update 广播的伪连接，测试结束自动清理。
func registerCaptureConn(t *testing.T, connID uint64, sessionID string, onUpdate func(SessionUpdate)) {
	t.Helper()
	connCallLock.Lock()
	connCallMap[connID] = func(method string, update any, _ *string) error {
		if method != "session/update" {
			return nil
		}
		if su, ok := update.(SessionUpdate); ok {
			onUpdate(su)
		}
		return nil
	}
	connCallLock.Unlock()

	sessionConnLock.Lock()
	sessionConnMap[sessionID] = append(sessionConnMap[sessionID], connID)
	sessionConnLock.Unlock()

	t.Cleanup(func() {
		connCallLock.Lock()
		delete(connCallMap, connID)
		connCallLock.Unlock()
		sessionConnLock.Lock()
		list := sessionConnMap[sessionID]
		for i, cid := range list {
			if cid == connID {
				sessionConnMap[sessionID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(sessionConnMap[sessionID]) == 0 {
			delete(sessionConnMap, sessionID)
		}
		sessionConnLock.Unlock()
	})
}

// TestRewindHelpExcludesHiddenCommand /help 文本同样过滤隐藏命令。
func TestRewindHelpExcludesHiddenCommand(t *testing.T) {
	help, ok := commandMaps["/help"]
	if !ok {
		t.Fatal("/help command not registered")
	}

	obj := &sessionObj{cwd: "/tmp/rewind-help-test", id: 9}
	sessionID := cwd2SessionID(obj.cwd, obj.id)

	var mu sync.Mutex
	var texts []string
	registerCaptureConn(t, 601, sessionID, func(su SessionUpdate) {
		inner, ok := su.Update.(SessionUpdateUpdate)
		if !ok || inner.SessionUpdate != "agent_message_chunk" {
			return
		}
		content, ok := inner.Content.(u.H)
		if !ok {
			return
		}
		if text, ok := content["text"].(string); ok {
			mu.Lock()
			texts = append(texts, text)
			mu.Unlock()
		}
	})

	if _, err := help.Function(obj, ""); err != nil {
		t.Fatalf("/help: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 1 {
		t.Fatalf("expected 1 help message, got %d", len(texts))
	}
	if !strings.Contains(texts[0], "**/help**") {
		t.Errorf("help output should list /help: %q", texts[0])
	}
	if strings.Contains(texts[0], "__alk_rewind") {
		t.Errorf("hidden command leaked into /help output: %q", texts[0])
	}
}

// TestRewindCommandRejectsInvalidArg 参数非法时立即报错（不触达 session/loop）。
func TestRewindCommandRejectsInvalidArg(t *testing.T) {
	cmd, ok := commandMaps["/__alk_rewind"]
	if !ok {
		t.Fatal("/__alk_rewind command not registered")
	}
	if _, err := cmd.Function(nil, "not-a-msgid"); err == nil {
		t.Fatal("expected error for invalid msgid")
	}
	if _, err := cmd.Function(nil, "msg_0"); err == nil {
		t.Fatal("expected error for zero msgid")
	}
}

// TestBroadcastRewindResult 成功/失败结果都广播给会话全部客户端；nil 结果不广播。
func TestBroadcastRewindResult(t *testing.T) {
	sessionID := "sess_1:/tmp/rewind-broadcast-test"

	var mu sync.Mutex
	byConn := map[uint64][]u.H{}
	conns := []uint64{701, 702}
	for _, cid := range conns {
		cid := cid
		registerCaptureConn(t, cid, sessionID, func(su SessionUpdate) {
			content, ok := su.Update.(u.H)
			if !ok {
				return
			}
			mu.Lock()
			byConn[cid] = append(byConn[cid], content)
			mu.Unlock()
		})
	}

	broadcastRewindResult(sessionID, &loop.RewindResult{MsgID: 42})
	broadcastRewindResult(sessionID, &loop.RewindResult{MsgID: 43, Err: errors.New("boom")})
	broadcastRewindResult(sessionID, nil)

	mu.Lock()
	defer mu.Unlock()
	for _, cid := range conns {
		events := byConn[cid]
		if len(events) != 2 {
			t.Fatalf("conn %d: expected 2 broadcasts, got %d", cid, len(events))
		}
		okEvent := events[0]
		if okEvent["sessionUpdate"] != "alk.cxykevin.top/session/rewind" {
			t.Errorf("conn %d: variant = %v", cid, okEvent["sessionUpdate"])
		}
		if okEvent["messageId"] != "msg_42" || okEvent["success"] != true {
			t.Errorf("conn %d: success event = %#v", cid, okEvent)
		}
		if _, has := okEvent["error"]; has {
			t.Errorf("conn %d: success event should not carry error", cid)
		}
		failEvent := events[1]
		if failEvent["messageId"] != "msg_43" || failEvent["success"] != false || failEvent["error"] != "boom" {
			t.Errorf("conn %d: failure event = %#v", cid, failEvent)
		}
	}
}
