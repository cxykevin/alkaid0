package actions

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestToolCallStreaming 验证原生 tool_calls 增量流式广播：
// mock 返回 delta.tool_calls 参数增量 → solver 增量解析 →
// OnHook 把部分参数写入 ToolCallingContext → SetCallback 限流广播
// tool_call_update（status=streaming）；审批自动通过后广播最终 tool_call_update。
func TestToolCallStreaming(t *testing.T) {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") != "true" {
		t.Skip("ALKAID0_DEBUG_MOCKSERVER not set, skipping test")
		return
	}

	openai.StartServerTask()
	setupConfigForTest()
	// 使用原生 toolcall 模型：mock 返回 delta.tool_calls 工具调用
	config.GlobalConfig.Model.Models[1] = cfgStructs.ModelConfig{
		ModelName:   "toolcall-native",
		ModelID:     "toolcall-native",
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

	// 发送任意 prompt；mock 原生 toolcall 模型第一次返回 tool_calls 流式响应，
	// 工具执行后返回普通文本以终止循环
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

	// 等待最终 tool_call（审批自动通过后 ExecuteToolCalls 完成触发）。
	// 断言 Status==completed：auto-approve 后 loop 在 handleWaitApprove 中先标记
	// ToolState=1 再发空回调（阻塞等待 SetCallback 读取 ToolState 广播），因此最终
	// tool_call_update 的 status 必然为 completed。修复前跳过该空回调，final 条目
	// 残留到下一轮 SendRequest（已重置 ToolState=0）才被 TakeFinalToolCalling 取出，
	// 会错误地广播为 pending——此断言即为该 bug 的回归测试。
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
		return upd.SessionUpdate == "tool_call_update" && upd.ToolCallID != "" && upd.Status == "completed"
	}
	if _, ok := waitForUpdate(calls2, matchFinal, 20*time.Second); !ok {
		t.Fatal("did not receive final tool_call_update event")
	}

	closeSession(sessionID)
}
