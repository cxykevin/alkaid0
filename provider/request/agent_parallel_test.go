package request

import (
	"strings"
	"testing"

	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
)

// newParallelTestSession 建库与 chat 行，返回处于 Idle 的会话。
func newParallelTestSession(t *testing.T, chatID uint32) (*storageStructs.Chats, func()) {
	t.Helper()
	db := setupTestDB(t)
	if err := db.Create(&storageStructs.Chats{ID: chatID}).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	session := &storageStructs.Chats{
		ID:           chatID,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}
	return session, func() { u.Unwrap(db.DB()).Close() }
}

// TestDetectParallelAgentLifecycle activate_agent / deactivate_agent 只要与其它
// 调用同批出现即命中（包括另一个生命周期调用）；单独出现或批内没有生命周期
// 调用不命中。返回的生命周期工具名决定纠正提示文案。
func TestDetectParallelAgentLifecycle(t *testing.T) {
	activate := `{"name":"activate_agent","id":"call_a","parameters":{"name":"demo","prompt":"work"}}`
	timeCall := `{"name":"time","id":"call_b","parameters":{}}`
	deactivate := `{"name":"deactivate_agent","id":"call_c","parameters":{"prompt":"done"}}`

	cases := []struct {
		name          string
		payload       string
		want          bool
		wantLifecycle string
	}{
		{"activate_with_time", "[" + activate + "," + timeCall + "]", true, "activate_agent"},
		{"two_activates", "[" + activate + `,{"name":"activate_agent","id":"call_d","parameters":{"name":"other","prompt":"work"}}]`, true, "activate_agent"},
		{"activate_with_deactivate", "[" + activate + "," + deactivate + "]", true, "activate_agent"},
		{"deactivate_with_time", "[" + deactivate + "," + timeCall + "]", true, "deactivate_agent"},
		{"two_deactivates", "[" + deactivate + `,{"name":"deactivate_agent","id":"call_d","parameters":{"prompt":"done"}}]`, true, "deactivate_agent"},
		{"lone_activate", "[" + activate + "]", false, ""},
		{"lone_deactivate", "[" + deactivate + "]", false, ""},
		{"no_lifecycle", "[" + timeCall + `,{"name":"read","id":"call_e","parameters":{"path":"a.go"}}]`, false, ""},
		{"empty", "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls, lifecycleTool, err := detectParallelAgentLifecycle(tc.payload)
			if err != nil {
				t.Fatalf("detectParallelAgentLifecycle() error = %v", err)
			}
			if got := len(calls) > 0; got != tc.want {
				t.Fatalf("detectParallelAgentLifecycle() hit = %v, want %v", got, tc.want)
			}
			if lifecycleTool != tc.wantLifecycle {
				t.Fatalf("lifecycle tool = %q, want %q", lifecycleTool, tc.wantLifecycle)
			}
		})
	}
}

// TestExecuteToolCallsRejectsParallelAgentLifecycle 命中时整批不执行，只注入
// role:tool 错误结果（模型下一轮可见），状态回到 Idle。
func TestExecuteToolCallsRejectsParallelAgentLifecycle(t *testing.T) {
	cases := []struct {
		name      string
		payload   string
		wantError string
	}{
		{"activate_with_time", `[{"name":"activate_agent","id":"call_a","parameters":{"name":"demo","prompt":"work"}},{"name":"time","id":"call_b","parameters":{}}]`, parallelAgentActivationError},
		{"two_activates", `[{"name":"activate_agent","id":"call_a","parameters":{"name":"demo","prompt":"work"}},{"name":"activate_agent","id":"call_b","parameters":{"name":"other","prompt":"work"}}]`, parallelAgentActivationError},
		{"activate_with_deactivate", `[{"name":"activate_agent","id":"call_a","parameters":{"name":"demo","prompt":"work"}},{"name":"deactivate_agent","id":"call_b","parameters":{"prompt":"done"}}]`, parallelAgentActivationError},
		{"deactivate_with_time", `[{"name":"deactivate_agent","id":"call_a","parameters":{"prompt":"done"}},{"name":"time","id":"call_b","parameters":{}}]`, parallelAgentDeactivationError},
		{"two_deactivates", `[{"name":"deactivate_agent","id":"call_a","parameters":{"prompt":"done"}},{"name":"deactivate_agent","id":"call_b","parameters":{"prompt":"again"}}]`, parallelAgentDeactivationError},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session, cleanup := newParallelTestSession(t, uint32(8100+i))
			defer cleanup()
			session.State = state.StateWaitApprove

			ok, err := ExecuteToolCalls(session, tc.payload)
			if err != nil {
				t.Fatalf("ExecuteToolCalls() error = %v, want nil", err)
			}
			if !ok {
				t.Error("ExecuteToolCalls() ok = false, want true")
			}
			if session.State != state.StateIdle {
				t.Fatalf("state = %v, want Idle", session.State)
			}
			// 生命周期工具的 PostHook 未执行：子代理状态不变。
			if session.CurrentAgentID != "" || session.NowAgent != "" {
				t.Errorf("lifecycle tool must not run: CurrentAgentID=%q NowAgent=%q",
					session.CurrentAgentID, session.NowAgent)
			}

			var msgs []storageStructs.Messages
			if err := session.DB.Where("chat_id = ? AND type = ?",
				session.ID, storageStructs.MessagesRoleTool).Find(&msgs).Error; err != nil {
				t.Fatalf("query tool messages: %v", err)
			}
			if len(msgs) != 1 {
				t.Fatalf("injected tool messages = %d, want 1", len(msgs))
			}
			// 批内每个 call 都要有配对结果，且带纠正提示。
			for _, want := range []string{"call_a", "call_b", tc.wantError} {
				if !strings.Contains(msgs[0].Delta, want) {
					t.Errorf("injected result missing %q: %s", want, msgs[0].Delta)
				}
			}
		})
	}
}

// TestExecuteToolCallsLoneLifecycleAgentNotRejected 单独调用生命周期工具
// 不触发并行拦截，走原来的执行路径。
func TestExecuteToolCallsLoneLifecycleAgentNotRejected(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"activate", `[{"name":"activate_agent","id":"call_a","parameters":{"name":"demo","prompt":"work"}}]`},
		{"deactivate", `[{"name":"deactivate_agent","id":"call_a","parameters":{"prompt":"done"}}]`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session, cleanup := newParallelTestSession(t, uint32(8200+i))
			defer cleanup()
			session.State = state.StateWaitApprove

			if _, err := ExecuteToolCalls(session, tc.payload); err != nil {
				t.Fatalf("ExecuteToolCalls() error = %v", err)
			}

			var msgs []storageStructs.Messages
			if err := session.DB.Where("chat_id = ? AND type = ?",
				session.ID, storageStructs.MessagesRoleTool).Find(&msgs).Error; err != nil {
				t.Fatalf("query tool messages: %v", err)
			}
			for _, m := range msgs {
				if strings.Contains(m.Delta, parallelAgentActivationError) ||
					strings.Contains(m.Delta, parallelAgentDeactivationError) {
					t.Fatalf("lone lifecycle call must not be rejected: %s", m.Delta)
				}
			}
		})
	}
}
