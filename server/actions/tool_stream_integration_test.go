package actions

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/index"
	u "github.com/cxykevin/alkaid0/utils"
)

// updateWireJSON 把更新序列化为线上 JSON，用于断言协议字段是否出现。
func updateWireJSON(t *testing.T, upd SessionUpdateUpdate) string {
	t.Helper()
	b, err := json.Marshal(upd)
	if err != nil {
		t.Fatalf("marshal update failed: %v", err)
	}
	return string(b)
}

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
	// 集成测试不经过 startup，显式加载内置工具以便请求携带 tools 定义。
	index.Load()
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
	// 协议不发送 rawInput：流式预览的参数同样只走 content。
	if wire := updateWireJSON(t, streamUpd); strings.Contains(wire, "\"rawInput\"") {
		t.Errorf("streaming tool_call_update 不应携带 rawInput: %s", wire)
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
	finalCall, ok := waitForUpdate(calls2, matchFinal, 20*time.Second)
	if !ok {
		t.Fatal("did not receive final tool_call_update event")
	}
	finalSu, ok := finalCall.Data.(SessionUpdate)
	if !ok {
		t.Fatalf("final update 类型错误: %T", finalCall.Data)
	}
	finalUpd, ok := finalSu.Update.(SessionUpdateUpdate)
	if !ok {
		t.Fatalf("final update 载荷类型错误: %T", finalSu.Update)
	}
	if wire := updateWireJSON(t, finalUpd); strings.Contains(wire, "\"rawInput\"") {
		t.Errorf("最终 tool_call_update 不应携带 rawInput（参数经 content 给出）: %s", wire)
	}
	// 最终 content 必须是模型实际发出的完整参数（mock 分片流式给出 path/target/text）：
	// 文本块按完整参数渲染，calling_info.args 同样完整。
	contentJSON, err := json.Marshal(finalUpd.Content)
	if err != nil {
		t.Fatalf("marshal content failed: %v", err)
	}
	gotArgs := map[string]any{}
	for _, block := range finalUpd.Content.([]u.H) {
		if block["type"] != structs.ToolCallingInfoType {
			continue
		}
		args, ok := block["args"].(map[string]any)
		if !ok {
			t.Fatalf("calling_info.args 不是对象: %s", contentJSON)
		}
		gotArgs = args
	}
	if len(gotArgs) == 0 {
		t.Fatalf("最终 content 缺少 calling_info：%s", contentJSON)
	}
	for key, want := range map[string]string{"path": "a.txt", "target": "x", "text": "hello"} {
		if got, _ := gotArgs[key].(string); got != want {
			t.Errorf("最终 calling_info.args[%s] = %q, want %q（实际 %s）", key, got, want, contentJSON)
		}
	}

	// 最终展示内容应已随消息落库：session/resume 回放按工具调用 ID 从这里重放
	// content（含 alk.cxykevin.top/calling_info 参数与 edit 的 Diffs 段）。
	sessLock.Lock()
	obj := sessions[sessionID]
	sessLock.Unlock()
	if obj == nil || obj.session == nil || obj.session.DB == nil {
		t.Fatal("session not registered")
	}
	var stored structs.Messages
	if err := obj.session.DB.Where("chat_id = ? AND tool_calling_json_string != ''", obj.session.ID).
		Order("id DESC").First(&stored).Error; err != nil {
		t.Fatalf("load tool calling message failed: %v", err)
	}
	if !strings.Contains(stored.ToolCallingContent, "call_mock_1") {
		t.Errorf("最终工具调用展示内容应落库（key=call_mock_1），实际 %q", stored.ToolCallingContent)
	}
	if !strings.Contains(stored.ToolCallingContent, "calling_info") {
		t.Errorf("落库内容应包含 alk.cxykevin.top/calling_info，实际 %q", stored.ToolCallingContent)
	}

	closeSession(sessionID)
}
