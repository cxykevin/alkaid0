package request

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
)

// e2eToolRecord 记录安全测试工具的一次回调。
type e2eToolRecord struct {
	id       string
	finished bool
}

// registerE2ETool 注册一个名为 name 的安全测试工具（OnHook 记录预览、PostHook 记录并返回固定结果）。
// 工具注册到全局 toolobj.ToolsList，t.Cleanup 恢复原状，避免污染同包其他测试。
func registerE2ETool(t *testing.T, name string, recs *[]e2eToolRecord) {
	toolobj.ToolsMu.Lock()
	orig, had := toolobj.ToolsList[name]
	toolobj.ToolsMu.Unlock()

	actions.AddTool(&toolobj.Tools{
		Scope:           "",
		Name:            name,
		ID:              name,
		UserDescription: "e2e safe test tool",
		Parameters: map[string]parser.ToolParameters{
			"input": {Type: parser.ToolTypeString, Required: true},
		},
		Hooks: []toolobj.Hook{{
			Scope: "",
			OnHook: toolobj.OnHookFunction{
				Func: func(session *storageStructs.Chats, args map[string]*any, passObjs []*any, toolID string) (bool, []*any, error) {
					*recs = append(*recs, e2eToolRecord{id: toolID, finished: false})
					return false, passObjs, nil
				},
			},
			PostHook: toolobj.PostHookFunction{
				Func: func(session *storageStructs.Chats, args map[string]*any, passObjs []*any) (bool, []*any, map[string]*any, error) {
					id := ""
					if p, ok := args["_id"]; ok && p != nil {
						if s, ok := (*p).(string); ok {
							id = s
						}
					}
					*recs = append(*recs, e2eToolRecord{id: id, finished: true})
					result := any("ok")
					return false, passObjs, map[string]*any{"result": &result}, nil
				},
			},
		}},
	})

	t.Cleanup(func() {
		toolobj.ToolsMu.Lock()
		if had {
			toolobj.ToolsList[name] = orig
		} else {
			delete(toolobj.ToolsList, name)
		}
		toolobj.ToolsMu.Unlock()
	})
}

// noopCallback SendRequest 的空回调（接收流式增量，忽略内容）。
func noopCallback(string, string, uint64, structs.Usage, *string) error { return nil }

// setupNativeE2EConfig 设置原生模式测试配置，ProviderURL 指向自建 httptest 服务器。
func setupNativeE2EConfig(url string) {
	config.GlobalConfigSwap(cfgStruct.Config{
		Model: cfgStruct.ModelsConfig{
			DefaultModelID: 1,
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName:         "native-e2e",
					ModelID:           "native-e2e",
					ProviderURL:       url,
					ProviderKey:       "mock-key",
					EnableToolCalling: true,
					EnableThinking:    false,
				},
			},
		},
		Agent: cfgStruct.AgentsConfig{
			GlobalPrompt: "You are a helpful assistant",
		},
	})
}

// sseChunk 输出一个 SSE data 帧。
func sseChunk(w http.ResponseWriter, resp structs.ChatCompletionResponse) {
	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// emitNativeToolCallsSSE 输出一轮原生 tool_calls 流式增量（id/name 首片 + 两片 arguments + 结束帧）。
func emitNativeToolCallsSSE(w http.ResponseWriter) {
	chunks := []structs.Message{
		{Role: structs.RoleAssistant, ToolCalls: []structs.StreamToolCall{{Index: 0, ID: "call_e2e_1", Type: "function", Function: &structs.StreamToolCallFunc{Name: "e2e_tool"}}}},
		{ToolCalls: []structs.StreamToolCall{{Index: 0, Function: &structs.StreamToolCallFunc{Arguments: `{"input": "hel`}}}},
		{ToolCalls: []structs.StreamToolCall{{Index: 0, Function: &structs.StreamToolCallFunc{Arguments: `lo"}`}}}},
	}
	for _, delta := range chunks {
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-e2e", Model: "native-e2e",
			Choices: []structs.Choice{{Index: 0, Delta: delta}},
		})
	}
	sseChunk(w, structs.ChatCompletionResponse{
		ID: "chatcmpl-e2e", Model: "native-e2e",
		Choices: []structs.Choice{{Index: 0, Delta: structs.Message{}, FinishReason: "tool_calls"}},
	})
}

// emitTextSSE 输出一段纯文本（工具已执行后的第二轮回复）。
func emitTextSSE(w http.ResponseWriter, text string) {
	sseChunk(w, structs.ChatCompletionResponse{
		ID: "chatcmpl-e2e", Model: "native-e2e",
		Choices: []structs.Choice{{Index: 0, Delta: structs.Message{Role: structs.RoleAssistant, Content: text}}},
	})
}

// reqHasToolReturn 判断请求体是否已含 <tools_return> 文本段（历史工具已执行）。
// 原生模式历史回放走原文本拼法，不产生 role:"tool" 消息。
func reqHasToolReturn(req structs.ChatCompletionRequest) bool {
	for _, m := range req.Messages {
		if m.Role == structs.RoleUser && strings.Contains(m.Content, "<tools_return>") {
			return true
		}
	}
	return false
}

// TestNativeSendRequest_ToolCalling 原生 tool_calls 全链路 e2e：
// 轮 1：SendRequest 流式解析原生 tool_calls → StateWaitApprove + 内部格式持久化（流式预览 OnHook 触发）；
// 执行：ExecuteToolCalls → 安全工具 PostHook → MessagesRoleTool 持久化；
// 轮 2：历史回放请求体 assistant 带 tool_calls、role:tool 带 tool_call_id；模型改回普通文本完成。
func TestNativeSendRequest_ToolCalling(t *testing.T) {
	initAgentsConsumer()
	var recs []e2eToolRecord
	registerE2ETool(t, "e2e_tool", &recs)

	var mu sync.Mutex
	var bodies []structs.ChatCompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var req structs.ChatCompletionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, req)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if reqHasToolReturn(req) {
			emitTextSSE(w, "e2e_tool executed")
		} else {
			emitNativeToolCallsSSE(w)
		}
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{ID: 9001, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "please call a tool"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}

	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     false,
		EnableScopes:   make(map[string]bool),
	}

	// 轮 1：原生 tool_calls → WaitApprove
	ok, err := SendRequest(context.Background(), session, noopCallback)
	if err != nil {
		t.Fatalf("round1 SendRequest: %v", err)
	}
	if !ok {
		t.Error("round1 should return ok=true (has tools)")
	}
	if session.State != state.StateWaitApprove {
		t.Fatalf("round1 state = %v, want WaitApprove", session.State)
	}

	// 内部格式持久化
	var assistMsg storageStructs.Messages
	if err := db.First(&assistMsg, session.CurrentMessageID).Error; err != nil {
		t.Fatalf("read assistant msg: %v", err)
	}
	var calls []map[string]any
	if err := json.Unmarshal([]byte(assistMsg.ToolCallingJSONString), &calls); err != nil {
		t.Fatalf("tool_calling_json_string not valid json: %q (%v)", assistMsg.ToolCallingJSONString, err)
	}
	if len(calls) != 1 || calls[0]["name"] != "e2e_tool" || calls[0]["id"] != "call_e2e_1" {
		t.Fatalf("unexpected internal format: %s", assistMsg.ToolCallingJSONString)
	}
	// 流式预览（OnHook，finished=false）至少触发一次
	hasPreview := false
	for _, r := range recs {
		if !r.finished {
			hasPreview = true
		}
	}
	if !hasPreview {
		t.Fatal("expected streaming preview OnHook (finished=false) during SendRequest")
	}

	// 执行工具
	if _, err := ExecuteToolCalls(session, assistMsg.ToolCallingJSONString); err != nil {
		t.Fatalf("ExecuteToolCalls: %v", err)
	}
	// PostHook（finished=true）触发并持久化 MessagesRoleTool
	var toolMsg storageStructs.Messages
	if err := db.Where("chat_id = ? AND type = ?", chat.ID, storageStructs.MessagesRoleTool).Order("id DESC").First(&toolMsg).Error; err != nil {
		t.Fatalf("tool result message not persisted: %v", err)
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(toolMsg.Delta), &results); err != nil {
		t.Fatalf("tool result delta not valid json: %q (%v)", toolMsg.Delta, err)
	}
	if len(results) != 1 || results[0]["name"] != "e2e_tool" || results[0]["id"] != "call_e2e_1" {
		t.Fatalf("unexpected tool result: %q", toolMsg.Delta)
	}

	// 轮 2：含 role:tool 回放，模型改回文本，无新工具
	if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
		t.Fatalf("round2 SendRequest: %v", err)
	}
	if session.State == state.StateWaitApprove {
		t.Error("round2 should not enter WaitApprove")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected >=2 request bodies, got %d", len(bodies))
	}
	round2 := bodies[len(bodies)-1]
	// 历史回放走原文本拼法：assistant 工具调用以 <tools> 段回放（无原生 tool_calls/tool_call_id），
	// 工具结果以 <tools_return> 段回放（user 角色）
	foundAssistantTools := false
	foundToolReturn := false
	noNativeToolCalls := true
	for _, m := range round2.Messages {
		if m.Role == structs.RoleAssistant {
			if strings.Contains(m.Content, "<tools>") && strings.Contains(m.Content, `"name":"e2e_tool"`) {
				foundAssistantTools = true
			}
			if len(m.ToolCalls) > 0 {
				noNativeToolCalls = false
			}
		}
		if m.Role == structs.RoleUser && strings.Contains(m.Content, "<tools_return>") && strings.Contains(m.Content, `{\"result\":\"ok\"}`) {
			foundToolReturn = true
		}
	}
	if !foundAssistantTools {
		t.Error("round2 request should replay assistant tool call as <tools> text segment")
	}
	if !noNativeToolCalls {
		t.Error("round2 request should NOT carry native tool_calls (no tool_call_id)")
	}
	if !foundToolReturn {
		t.Error("round2 request should replay tool result as <tools_return> text segment")
	}
}

// TestNativeSendRequest_LegacyRejection 原生模式下模型仍输出 <tools> 标签 → 打回：
// 返回 (false, nil)、占位 assistant 消息被删除、注入原生格式纠正消息。
func TestNativeSendRequest_LegacyRejection(t *testing.T) {
	initAgentsConsumer()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 原生模式下模型错误输出 <tools> 标签（行首）→ 应被打回
		emitTextSSE(w, "\n<tools>[{\"name\":\"e2e_tool\",\"id\":\"c\",\"parameters\":{\"input\":\"x\"}}]</tools>")
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{ID: 9002, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "please call a tool"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}

	// InTestFlag=true 跳过工具装载（打回发生在解析 <tools> 标签时，无需真实工具）
	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     true,
		EnableScopes:   make(map[string]bool),
	}

	ok, err := SendRequest(context.Background(), session, noopCallback)
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if ok {
		t.Error("legacy <tools> response should be rejected (ok=false)")
	}

	// 占位 assistant 消息被删除
	var agentMsgs []storageStructs.Messages
	if err := db.Where("chat_id = ? AND type = ?", chat.ID, storageStructs.MessagesRoleAgent).Find(&agentMsgs).Error; err != nil {
		t.Fatalf("query agent msgs: %v", err)
	}
	if len(agentMsgs) != 0 {
		t.Errorf("placeholder assistant message should be deleted, got %d", len(agentMsgs))
	}

	// 注入原生格式纠正消息（user 类型，含原生 function-calling 声明）
	var msgs []storageStructs.Messages
	if err := db.Where("chat_id = ?", chat.ID).Order("id ASC").Find(&msgs).Error; err != nil {
		t.Fatalf("query msgs: %v", err)
	}
	if len(msgs) != 2 { // 原 user + 纠正消息
		t.Fatalf("expected 2 messages (user + correction), got %d", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Type != storageStructs.MessagesRoleUser {
		t.Errorf("correction message should be user type, got %d", last.Type)
	}
	if !strings.Contains(last.Delta, "native function-calling API") {
		t.Errorf("correction message should mention native function-calling, got: %q", last.Delta)
	}
}
