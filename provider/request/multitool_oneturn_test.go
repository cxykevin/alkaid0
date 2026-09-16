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

	reqStructs "github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
)

// multiToolPayload 构造一轮内的多个工具调用（同一 assistant 消息携带多个 tool_calls）。
func multiToolPayload(toolName string, inputs ...string) string {
	items := make([]string, 0, len(inputs))
	for i, in := range inputs {
		items = append(items, fmt.Sprintf(
			"{\"name\":%q,\"id\":\"call_%d\",\"parameters\":{\"input\":%q}}", toolName, i+1, in))
	}
	return "[" + strings.Join(items, ",") + "]"
}

// TestExecuteToolCalls_MultipleCallsInOneTurn 回归：一轮中发起多个工具调用时，
// 每个调用都必须真正执行并产生各自的 role:tool 结果。
//
// 历史缺陷：ExecuteToolCalls 把存储的每个调用喂给 NativeToolCallAccumulator 时
// 未设置 Index（全部为零值）。累积器以 index 为键维护单次调用的流式状态，导致一轮
// 中的多个调用塌缩到同一个 state——首个调用 finalized 之后，其余调用在 AddDelta 中
// 命中"已 finalize"分支被静默丢弃：既不执行 PostHook，也不产生 role:tool 结果。
// 表现为"同一轮并行读取多个文件只成功一个"，且 assistant 的 tool_calls 缺少配对结果，
// 严格校验的 Provider 会直接 400。
func TestExecuteToolCalls_MultipleCallsInOneTurn(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	if err := db.Create(&storageStructs.Chats{ID: 7501}).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}

	var recs []e2eToolRecord
	registerE2ETool(t, "multi_e2e_tool", &recs)

	payload := multiToolPayload("multi_e2e_tool", "a", "b", "c")
	session := &storageStructs.Chats{
		ID:           7501,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}
	if _, err := ExecuteToolCalls(session, payload); err != nil {
		t.Fatalf("ExecuteToolCalls: %v", err)
	}

	// 1) 三个调用都应各执行一次 PostHook，且互不覆盖
	finished := make(map[string]int)
	for _, rec := range recs {
		if rec.finished {
			finished[rec.id]++
		}
	}
	for _, id := range []string{"call_1", "call_2", "call_3"} {
		if finished[id] != 1 {
			t.Errorf("调用 %s 应恰好执行一次 PostHook，实际 %d 次（全部记录 %v）", id, finished[id], recs)
		}
	}

	// 2) 结果持久化为一条 role:tool 消息，包含全部三个调用且按调用顺序配对
	var toolMsgs []storageStructs.Messages
	if err := db.Where("chat_id = ? AND type = ?", 7501, storageStructs.MessagesRoleTool).
		Find(&toolMsgs).Error; err != nil {
		t.Fatalf("query tool messages: %v", err)
	}
	if len(toolMsgs) != 1 {
		t.Fatalf("期望 1 条 role:tool 结果消息，实际 %d", len(toolMsgs))
	}
	results := parseToolResults(t, toolMsgs[0].Delta)
	if len(results) != 3 {
		t.Fatalf("期望 3 条工具结果，实际 %d（原文 %s）", len(results), toolMsgs[0].Delta)
	}
	for i, want := range []string{"call_1", "call_2", "call_3"} {
		if results[i].ID != want {
			t.Errorf("第 %d 条结果 id 应为 %s，实际 %s", i+1, want, results[i].ID)
		}
		if results[i].Name != "multi_e2e_tool" {
			t.Errorf("第 %d 条结果 name 应为 multi_e2e_tool，实际 %s", i+1, results[i].Name)
		}
		if results[i].Return == "" {
			t.Errorf("第 %d 条结果（%s）不应为空", i+1, results[i].ID)
		}
	}

	// 3) 执行结束恢复 Idle
	if session.State != 0 {
		t.Errorf("工具执行结束后状态应为 Idle，实际 %d", session.State)
	}
}

// TestExecuteToolCalls_SingleCallUnchanged 单调用场景不受影响。
func TestExecuteToolCalls_SingleCallUnchanged(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	if err := db.Create(&storageStructs.Chats{ID: 7502}).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}

	var recs []e2eToolRecord
	registerE2ETool(t, "single_e2e_tool", &recs)

	session := &storageStructs.Chats{
		ID:           7502,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}
	if _, err := ExecuteToolCalls(session, multiToolPayload("single_e2e_tool", "only")); err != nil {
		t.Fatalf("ExecuteToolCalls: %v", err)
	}

	finished := 0
	for _, rec := range recs {
		if rec.finished {
			finished++
		}
	}
	if finished != 1 {
		t.Errorf("单调用应执行一次 PostHook，实际 %d", finished)
	}
}

// toolResultItem 存储层工具结果项（[{"name","id","return"}]）。
type toolResultItem struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Return string `json:"return"`
}

// parseToolResults 解析 role:tool 消息的工具结果列表。
func parseToolResults(t *testing.T, delta string) []toolResultItem {
	t.Helper()
	var results []toolResultItem
	if err := json.Unmarshal([]byte(delta), &results); err != nil {
		t.Fatalf("解析工具结果失败: %v（原文 %s）", err, delta)
	}
	return results
}

// emitNativeParallelToolCallsSSE 输出一轮含多个并行 tool_calls 的原生流式响应：
// 先按 index 逐个到达 id/name，再按 index 分片到达 arguments，最后以 finish_reason
// tool_calls 收尾。形态与真实 Provider 的 delta.tool_calls 一致。
func emitNativeParallelToolCallsSSE(w http.ResponseWriter, toolName string, ids []string, args []string) {
	chunks := make([]reqStructs.Message, 0, len(ids)*3)
	for i := range ids {
		chunks = append(chunks, reqStructs.Message{Role: reqStructs.RoleAssistant, ToolCalls: []reqStructs.StreamToolCall{{
			Index: i, ID: ids[i], Type: "function",
			Function: &reqStructs.StreamToolCallFunc{Name: toolName},
		}}})
	}
	for i := range ids {
		half := len(args[i]) / 2
		chunks = append(chunks,
			reqStructs.Message{ToolCalls: []reqStructs.StreamToolCall{{Index: i, Function: &reqStructs.StreamToolCallFunc{Arguments: args[i][:half]}}}},
			reqStructs.Message{ToolCalls: []reqStructs.StreamToolCall{{Index: i, Function: &reqStructs.StreamToolCallFunc{Arguments: args[i][half:]}}}})
	}
	for _, delta := range chunks {
		sseChunk(w, reqStructs.ChatCompletionResponse{
			ID: "chatcmpl-e2e", Model: "native-e2e",
			Choices: []reqStructs.Choice{{Index: 0, Delta: delta}},
		})
	}
	sseChunk(w, reqStructs.ChatCompletionResponse{
		ID: "chatcmpl-e2e", Model: "native-e2e",
		Choices: []reqStructs.Choice{{Index: 0, Delta: reqStructs.Message{}, FinishReason: "tool_calls"}},
	})
}

// TestNativeSendRequest_ParallelToolCalls 端到端回归（对应"同一轮并行读取多个文件"）：
// Provider 一轮返回两个原生 tool_calls → 内部格式两条调用 → ExecuteToolCalls 两个
// PostHook 都执行、结果成对持久化 → 轮 2 请求体回放两个 tool_calls 且都有配对结果。
func TestNativeSendRequest_ParallelToolCalls(t *testing.T) {
	initAgentsConsumer()
	var recs []e2eToolRecord
	registerE2ETool(t, "parallel_e2e_tool", &recs)

	var mu sync.Mutex
	var bodies []reqStructs.ChatCompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var req reqStructs.ChatCompletionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, req)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if reqHasToolReturn(req) {
			emitTextSSE(w, "parallel tools executed")
		} else {
			emitNativeParallelToolCallsSSE(w, "parallel_e2e_tool",
				[]string{"call_p1", "call_p2"},
				[]string{"{\"input\":\"first\"}", "{\"input\":\"second\"}"})
		}
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{ID: 9101, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "please call two tools"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}

	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		EnableScopes:   make(map[string]bool),
	}

	// 轮 1：两个原生 tool_calls → WaitApprove + 内部格式两条调用
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

	var assistMsg storageStructs.Messages
	if err := db.First(&assistMsg, session.CurrentMessageID).Error; err != nil {
		t.Fatalf("read assistant msg: %v", err)
	}
	var calls []map[string]any
	if err := json.Unmarshal([]byte(assistMsg.ToolCallingJSONString), &calls); err != nil {
		t.Fatalf("tool_calling_json_string not valid json: %q (%v)", assistMsg.ToolCallingJSONString, err)
	}
	if len(calls) != 2 {
		t.Fatalf("期望内部格式 2 条调用，实际 %d：%s", len(calls), assistMsg.ToolCallingJSONString)
	}

	// 执行工具：两个调用都必须真正执行
	if _, err := ExecuteToolCalls(session, assistMsg.ToolCallingJSONString); err != nil {
		t.Fatalf("ExecuteToolCalls: %v", err)
	}
	finished := make(map[string]int)
	for _, rec := range recs {
		if rec.finished {
			finished[rec.id]++
		}
	}
	for _, id := range []string{"call_p1", "call_p2"} {
		if finished[id] != 1 {
			t.Errorf("调用 %s 应恰好执行一次 PostHook，实际 %d 次（全部记录 %v）", id, finished[id], recs)
		}
	}

	// 两个结果持久化为同一条 role:tool 消息
	var toolMsg storageStructs.Messages
	if err := db.Where("chat_id = ? AND type = ?", chat.ID, storageStructs.MessagesRoleTool).
		Order("id DESC").First(&toolMsg).Error; err != nil {
		t.Fatalf("tool result message not persisted: %v", err)
	}
	results := parseToolResults(t, toolMsg.Delta)
	if len(results) != 2 {
		t.Fatalf("期望 2 条工具结果，实际 %d（原文 %s）", len(results), toolMsg.Delta)
	}
	for i, want := range []string{"call_p1", "call_p2"} {
		if results[i].ID != want {
			t.Errorf("第 %d 条结果 id 应为 %s，实际 %s", i+1, want, results[i].ID)
		}
	}

	// 轮 2：回放两个 tool_calls 且都有配对结果
	if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
		t.Fatalf("round2 SendRequest: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected >=2 request bodies, got %d", len(bodies))
	}
	round2 := bodies[len(bodies)-1]
	callIDs := map[string]bool{}
	resultIDs := map[string]bool{}
	for _, m := range round2.Messages {
		if m.Role == reqStructs.RoleAssistant {
			for _, tc := range m.ToolCalls {
				callIDs[tc.ID] = true
			}
		}
		if m.Role == reqStructs.RoleTool && m.ToolCallID != "" {
			resultIDs[m.ToolCallID] = true
		}
	}
	for _, id := range []string{"call_p1", "call_p2"} {
		if !callIDs[id] {
			t.Errorf("轮 2 请求体缺少 assistant tool_call %s", id)
		}
		if !resultIDs[id] {
			t.Errorf("轮 2 请求体缺少 tool_call %s 的配对结果（并行工具调用会产生没有结果的悬空 tool_call）", id)
		}
	}
}
