package actions

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/tools/index"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestToolCallStreaming 验证工具调用增量流式广播：
// mock toolcall-chat 逐 word 流式返回 <tools> JSON（参数逐 token 增量到达）→ solver 增量解析 →
// OnHook 把部分参数写入 ToolCallingContext → SetCallback 限流广播
// tool_call_update（status=streaming）；审批自动通过后广播最终 tool_call_update。
func TestToolCallStreaming(t *testing.T) {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") != "true" {
		t.Skip("ALKAID0_DEBUG_MOCKSERVER not set, skipping test")
		return
	}

	openai.StartServerTask()
	setupConfigForTest()
	// 注册所有内置工具（index.Load 在服务器启动时调用；测试环境需手动触发，
	// 否则 toolobj.ToolsList 为空、ToolsSolver 解析不到任何工具）
	index.Load()
	// 使用 toolcall-chat 模型：mock 直接流式返回固定的 <tools> 工具调用
	config.GlobalConfig.Model.Models[1] = cfgStructs.ModelConfig{
		ModelName:   "toolcall-chat",
		ModelID:     "toolcall-chat",
		ProviderURL: openai.BaseURL,
		ProviderKey: "test-key",
	}

	// 清理全局状态，避免与其他测试互相影响
	sessions = map[string]*sessionObj{}
	sessLock = &sync.Mutex{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	connCallLock = &sync.Mutex{}
	sessionConnMap = map[string][]uint64{}
	sessionConnLock = &sync.Mutex{}
	bindedSessionOnConn = map[uint64][]string{}

	tmpDir, err := os.MkdirTemp("", "alkaid0_toolstream_test_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	calls2 := make(chan ReceivedCall, 300)
	sessionID := newTitleTestSession(t, tmpDir, calls2)

	// 发送任意 prompt；mock toolcall-chat 无视内容，第一次返回 <tools> 流式响应，
	// 工具执行后（请求含 <tools_return>）返回普通文本以终止循环
	_, err = SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "please call a tool"}}}, nil, 1)
	if err != nil {
		t.Fatalf("SessionPrompt failed: %v", err)
	}

	// 等待收到 tool_call_streaming 事件（限流后仍至少一次）
	var gotStreaming bool
	var streamUpd SessionUpdateUpdate
	deadline := time.After(20 * time.Second)
	collected := make([]string, 0)
	for !gotStreaming {
		select {
		case v := <-calls2:
			if v.Name != "session/update" {
				continue
			}
			su, ok := v.Data.(SessionUpdate)
			if !ok {
				continue
			}
			upd, ok2 := su.Update.(SessionUpdateUpdate)
			if !ok2 {
				continue
			}
			collected = append(collected, upd.SessionUpdate)
			if upd.SessionUpdate == "tool_call_update" &&
				upd.Status == "streaming" && upd.ToolCallID != "" {
				gotStreaming = true
				streamUpd = upd
			}
		case <-deadline:
			t.Fatalf("timeout waiting for tool_call_streaming; collected events: %v", collected)
		}
	}
	if streamUpd.Content == nil {
		t.Fatal("streaming event content should not be nil")
	}
	t.Logf("received streaming event: toolCallId=%s kind=%s", streamUpd.ToolCallID, streamUpd.Kind)

	// 等待最终 tool_call（审批自动通过后 ExecuteToolCalls 完成触发）
	matchFinal := func(rc ReceivedCall) bool {
		if rc.Name != "session/update" {
			return false
		}
		su, ok := rc.Data.(SessionUpdate)
		if !ok {
			return false
		}
		upd, ok2 := su.Update.(SessionUpdateUpdate)
		if !ok2 {
			return false
		}
		// Status 可能是 completed/pending（approve 后 ToolState 有竞态），只断言事件与调用 ID
		return upd.SessionUpdate == "tool_call_update" && upd.ToolCallID != ""
	}
	if _, ok := waitForUpdate(calls2, matchFinal, 20*time.Second); !ok {
		t.Fatal("did not receive final tool_call_update event")
	}

	closeSession(sessionID)
}
