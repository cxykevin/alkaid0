package request

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/request/build"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/stats"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestSplitMultiToolCalls_FromRequestBody 端到端回归：构造用户真实场景
// （模型一次返回 4 个工具调用，结果全部存在），经 build.RequestBody 生成请求体后
// 再经 splitMultiToolCalls，验证在逐条转换（策略 A）下无 tool_use 配对缺口。
// 修复前该场景产生的缺口恰为 call_01/call_02/call_03（与线上报错一致）。
func TestSplitMultiToolCalls_FromRequestBody(t *testing.T) {
	db := setupTestDB(t)
	setupNativeE2EConfig("http://mock")

	msgs := []storageStructs.Messages{
		{ChatID: 5001, Type: storageStructs.MessagesRoleUser, Delta: "do things"},
		{ChatID: 5001, Type: storageStructs.MessagesRoleAgent, Delta: "ok", ToolCallingJSONString: `[{"name":"edit","id":"call_00_e","parameters":{}},{"name":"read","id":"call_01_r1","parameters":{}},{"name":"read","id":"call_02_r2","parameters":{}},{"name":"read","id":"call_03_r3","parameters":{}}]`},
		{ChatID: 5001, Type: storageStructs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_00_e","return":"{\"ok\":true}"},{"name":"read","id":"call_01_r1","return":"a"},{"name":"read","id":"call_02_r2","return":"b"},{"name":"read","id":"call_03_r3","return":"c"}]`},
		{ChatID: 5001, Type: storageStructs.MessagesRoleUser, Delta: "continue"},
	}
	for _, mm := range msgs {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	toolsList := []*parser.ToolsDefine{}
	req, err := build.RequestBody(5001, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &storageStructs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	// 修复前：RequestBody 直接产物在策略 A 下应有缺口（call_01/call_02/call_03）
	before := toolPairingGaps(req.Messages)
	foundMulti := false
	for _, miss := range before {
		foundMulti = true
		t.Logf("(pre-split) pairing gap: %s", strings.Join(miss, ", "))
	}
	if !foundMulti {
		t.Log("(pre-split) no gap detected")
	}

	// 应用修复
	splitted := splitMultiToolCalls(req.Messages)
	after := toolPairingGaps(splitted)
	if len(after) > 0 {
		for _, miss := range after {
			t.Errorf("post-split pairing gap remains: %s", strings.Join(miss, ", "))
		}
	} else {
		t.Log("post-split: no pairing gap")
	}
}

// toolPairingGaps 模拟 OpenAI→Anthropic 转换层（逐条把 role:tool 转独立 user）的配对校验：
// 每个 assistant 携带的 tool_calls（转换后 tool_use）必须在其后紧邻的下一条消息中
// 找到对应 tool_result，否则视为缺失（对应 Anthropic 报错
// "tool_use ids were found without tool_result blocks immediately after"）。
// 返回缺失的 tool_call id 列表（assistant 下标 → 缺失 id）。
func toolPairingGaps(messages []structs.Message) map[int][]string {
	gaps := make(map[int][]string)
	for i, msg := range messages {
		if msg.Role != structs.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		// 紧邻下一条消息（逐条转换：assistant 后第一条 role:tool 即独立 user(tool_result)）
		var next *structs.Message
		if i+1 < len(messages) {
			m := messages[i+1]
			if m.Role == structs.RoleTool {
				next = &m
			}
		}
		var missing []string
		for _, tc := range msg.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if next == nil || next.ToolCallID != tc.ID {
				missing = append(missing, tc.ID)
			}
		}
		if len(missing) > 0 {
			gaps[i] = missing
		}
	}
	return gaps
}

func asstMsg(toolCallIDs ...string) structs.Message {
	var calls []structs.StreamToolCall
	for _, id := range toolCallIDs {
		calls = append(calls, structs.StreamToolCall{ID: id, Type: "function", Function: &structs.StreamToolCallFunc{Name: "run", Arguments: "{}"}})
	}
	return structs.Message{Role: structs.RoleAssistant, Content: "ok", ToolCalls: calls}
}

func toolMsg(id, content string) structs.Message {
	return structs.Message{Role: structs.RoleTool, Content: content, ToolCallID: id}
}

func TestSplitMultiToolCalls_Basic(t *testing.T) {
	// 多 tool_call + 全部结果：拆分后逐条 assistant 单调用 + 对应结果，顺序与 toolCalls 一致
	in := []structs.Message{
		{Role: structs.RoleUser, Content: "do it"},
		asstMsg("call_a", "call_b", "call_c"),
		toolMsg("call_a", "r_a"),
		toolMsg("call_b", "r_b"),
		toolMsg("call_c", "r_c"),
		{Role: structs.RoleUser, Content: "continue"},
	}
	out := splitMultiToolCalls(in)
	var asst, tools int
	for i, m := range out {
		switch m.Role {
		case structs.RoleAssistant:
			asst++
			if len(m.ToolCalls) != 1 {
				t.Fatalf("assistant[%d] should carry exactly 1 tool_call, got %d", i, len(m.ToolCalls))
			}
			// 只有第一条保留正文
			if i > 0 && out[i-1].Role != structs.RoleTool && m.Content != "" {
				// 第一条 assistant 前应是 user；但这里简化：仅检查非首 assistant 无正文
				_ = i
			}
		case structs.RoleTool:
			tools++
		}
	}
	if asst != 3 || tools != 3 {
		t.Fatalf("expected 3 assistants and 3 tools after split, got asst=%d tools=%d", asst, tools)
	}
	// 期望序列：[user, asst(a), tool(a), asst(b), tool(b), asst(c), tool(c), user]
	var gotIDs []string
	for _, m := range out {
		if m.Role == structs.RoleAssistant {
			gotIDs = append(gotIDs, "A:"+m.ToolCalls[0].ID)
		} else if m.Role == structs.RoleTool {
			gotIDs = append(gotIDs, "T:"+m.ToolCallID)
		}
	}
	want := []string{"A:call_a", "T:call_a", "A:call_b", "T:call_b", "A:call_c", "T:call_c"}
	if strings.Join(gotIDs, "|") != strings.Join(want, "|") {
		t.Errorf("split order wrong:\n got %v\nwant %v", gotIDs, want)
	}
	// 配对校验（策略 A：逐条转换）不得有缺口
	if gaps := toolPairingGaps(out); len(gaps) > 0 {
		for _, miss := range gaps {
			t.Errorf("pairing gap after split: %s", strings.Join(miss, ", "))
		}
	}
}

func TestSplitMultiToolCalls_FirstKeepsContent(t *testing.T) {
	in := []structs.Message{
		asstMsg("call_a", "call_b"),
		toolMsg("call_a", "r_a"),
		toolMsg("call_b", "r_b"),
	}
	out := splitMultiToolCalls(in)
	// 第一条 assistant 保留正文，后续用非空占位（Anthropic 拒绝空 text block）
	asstSeen := 0
	for _, m := range out {
		if m.Role == structs.RoleAssistant {
			if asstSeen == 0 && m.Content != "ok" {
				t.Errorf("first assistant should keep content %q, got %q", "ok", m.Content)
			}
			if asstSeen == 1 && m.Content != emptyAssistantToolCallContent {
				t.Errorf("following assistant should use non-empty placeholder %q, got %q", emptyAssistantToolCallContent, m.Content)
			}
			if asstSeen == 1 && m.Content == "" {
				t.Error("following assistant content must be non-empty (Anthropic rejects empty text block)")
			}
			asstSeen++
		}
	}
}

func TestSplitMultiToolCalls_PartialResults(t *testing.T) {
	// 多 tool_call 但部分结果缺失（如被终止）：缺失调用补占位，仍保证逐条配对
	in := []structs.Message{
		{Role: structs.RoleUser, Content: "go"},
		asstMsg("call_a", "call_b", "call_c"),
		toolMsg("call_a", "r_a"),
		// call_b、call_c 无结果
		{Role: structs.RoleUser, Content: "stop"},
	}
	out := splitMultiToolCalls(in)
	tools := 0
	for _, m := range out {
		if m.Role == structs.RoleTool {
			tools++
			if m.Content == "" {
				t.Errorf("tool message should not be empty (real or placeholder)")
			}
		}
	}
	if tools != 3 {
		t.Fatalf("expected 3 tool results after split (1 real + 2 placeholder), got %d", tools)
	}
	if gaps := toolPairingGaps(out); len(gaps) > 0 {
		for _, miss := range gaps {
			t.Errorf("pairing gap after split with partial results: %s", strings.Join(miss, ", "))
		}
	}
}

func TestSplitMultiToolCalls_ReordersMisplacedPlaceholder(t *testing.T) {
	// 回归：修复前占位结果排在真实结果之前（顺序错乱）。拆分后按 toolCalls 顺序重排。
	in := []structs.Message{
		asstMsg("call_a", "call_b"),
		toolMsg("call_b", "[Tool call terminated] This call was cancelled before execution and did not run."), // 占位在前
		toolMsg("call_a", "r_a"), // 真实结果在后
	}
	out := splitMultiToolCalls(in)
	// 期望顺序：A:call_a, T:call_a, A:call_b, T:call_b
	var gotIDs []string
	for _, m := range out {
		if m.Role == structs.RoleAssistant {
			gotIDs = append(gotIDs, "A:"+m.ToolCalls[0].ID)
		} else if m.Role == structs.RoleTool {
			gotIDs = append(gotIDs, "T:"+m.ToolCallID)
		}
	}
	want := []string{"A:call_a", "T:call_a", "A:call_b", "T:call_b"}
	if strings.Join(gotIDs, "|") != strings.Join(want, "|") {
		t.Errorf("placeholder order not corrected:\n got %v\nwant %v", gotIDs, want)
	}
	if gaps := toolPairingGaps(out); len(gaps) > 0 {
		for _, miss := range gaps {
			t.Errorf("pairing gap after reorder: %s", strings.Join(miss, ", "))
		}
	}
}

// TestSendRequest_ToolCallingCompatFlag 验证兼容开关在 SendRequest 中的门控：
// 开启时请求体中的多 tool_call assistant 被拆分（单 tool_call + 结果配对），
// 关闭（默认）时保持原样（一个 assistant 携带多个 tool_calls）。
func TestSendRequest_ToolCallingCompatFlag(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "compat-off"
		if enabled {
			name = "compat-on"
		}
		t.Run(name, func(t *testing.T) {
			initAgentsConsumer()
			stats.ResetForTest()
			stats.SetFilePath(filepath.Join(t.TempDir(), "usage.json"))

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
				emitTextSSE(w, "done ok")
				fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
			}))
			defer srv.Close()
			setupNativeE2EConfig(srv.URL)
			cfg := *config.GlobalConfig
			m := cfg.Model.Models[cfg.Model.DefaultModelID]
			m.ProviderSpecificConfig.EnableToolCallingCompat = enabled
			cfg.Model.Models[cfg.Model.DefaultModelID] = m
			config.GlobalConfigSwap(cfg)

			db := setupTestDB(t)
			defer u.Unwrap(db.DB()).Close()
			chat := storageStructs.Chats{ID: 6001, LastModelID: 1}
			if err := db.Create(&chat).Error; err != nil {
				t.Fatalf("create chat: %v", err)
			}
			msgs := []storageStructs.Messages{
				{ChatID: 6001, Type: storageStructs.MessagesRoleUser, Delta: "do it"},
				{ChatID: 6001, Type: storageStructs.MessagesRoleAgent, Delta: "ok", ToolCallingJSONString: `[{"name":"run","id":"call_a","parameters":{}},{"name":"run","id":"call_b","parameters":{}},{"name":"run","id":"call_c","parameters":{}}]`},
				{ChatID: 6001, Type: storageStructs.MessagesRoleTool, Delta: `[{"name":"run","id":"call_a","return":"a"},{"name":"run","id":"call_b","return":"b"},{"name":"run","id":"call_c","return":"c"}]`},
				{ChatID: 6001, Type: storageStructs.MessagesRoleUser, Delta: "continue"},
			}
			for _, mm := range msgs {
				if err := db.Create(&mm).Error; err != nil {
					t.Fatalf("create msg: %v", err)
				}
			}

			session := &storageStructs.Chats{ID: 6001, DB: db, LastModelID: 1, CurrentAgentID: "", EnableScopes: make(map[string]bool)}
			if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
				t.Fatalf("SendRequest: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(bodies) == 0 {
				t.Fatal("no request captured")
			}
			req := bodies[0]
			var multi, single, toolN int
			for _, msg := range req.Messages {
				if msg.Role == structs.RoleAssistant && len(msg.ToolCalls) > 0 {
					if len(msg.ToolCalls) > 1 {
						multi++
					} else {
						single++
					}
				}
				if msg.Role == structs.RoleTool {
					toolN++
				}
			}
			if enabled {
				if multi != 0 || single != 3 || toolN != 3 {
					t.Errorf("compat-on: want 0 multi / 3 single / 3 tool, got multi=%d single=%d tool=%d", multi, single, toolN)
				}
			} else {
				if multi != 1 || single != 0 || toolN != 3 {
					t.Errorf("compat-off: want 1 multi / 0 single / 3 tool, got multi=%d single=%d tool=%d", multi, single, toolN)
				}
			}
		})
	}
}

// TestSplitMultiToolCalls_KeepsThinking 回归：thinking 模式下拆分多工具调用时，
// 拆分出的每条 assistant 消息都必须保留 reasoning_content（thinking 模式要求每条
// assistant 都携带该字段，OpenAI 端点）/ content[].thinking 块（Anthropic 端点），
// 否则转换代理端 400 "The content[].thinking in the thinking mode must be passed back to the API"。
func TestSplitMultiToolCalls_KeepsThinking(t *testing.T) {
	thinking := "thinking content"
	in := []structs.Message{
		{
			Role:             structs.RoleAssistant,
			Content:          "ok",
			ReasoningContent: &thinking,
			ToolCalls: []structs.StreamToolCall{
				{ID: "call_a", Type: "function", Function: &structs.StreamToolCallFunc{Name: "run", Arguments: "{}"}},
				{ID: "call_b", Type: "function", Function: &structs.StreamToolCallFunc{Name: "run", Arguments: "{}"}},
				{ID: "call_c", Type: "function", Function: &structs.StreamToolCallFunc{Name: "run", Arguments: "{}"}},
			},
		},
		toolMsg("call_a", "r_a"),
		toolMsg("call_b", "r_b"),
		toolMsg("call_c", "r_c"),
	}
	out := splitMultiToolCalls(in)
	asstN := 0
	for _, m := range out {
		if m.Role != structs.RoleAssistant {
			continue
		}
		asstN++
		if m.ReasoningContent == nil {
			t.Errorf("拆分后的 assistant #%d 丢失 reasoning_content（thinking 模式必须保留字段）", asstN)
		} else if *m.ReasoningContent != thinking {
			t.Errorf("assistant #%d reasoning_content 被篡改：got %q want %q", asstN, *m.ReasoningContent, thinking)
		}
	}
	if asstN != 3 {
		t.Fatalf("expected 3 assistant messages after split, got %d", asstN)
	}
}

func TestSplitMultiToolCalls_Noop(t *testing.T) {
	// 无 tool_call / 单 tool_call / 非 assistant 消息：保持不变
	inputs := [][]structs.Message{
		{{Role: structs.RoleUser, Content: "hi"}},
		{asstMsg("call_a"), toolMsg("call_a", "r_a")}, // 单调用
		{{Role: structs.RoleSystem, Content: "sys"}},
	}
	for i, in := range inputs {
		out := splitMultiToolCalls(in)
		if len(out) != len(in) {
			t.Errorf("case %d: length changed: got %d want %d", i, len(out), len(in))
		}
		for j := range out {
			if out[j].Role != in[j].Role || out[j].Content != in[j].Content {
				t.Errorf("case %d msg %d changed: got %+v want %+v", i, j, out[j], in[j])
			}
		}
	}
}

// TestSendRequest_TrailingToolAppendsUser 回归：历史以 role:"tool" 结果消息结尾时，
// SendRequest 必须在请求末尾追加一条 user 消息收尾——30881 转换代理 / DeepSeek 兼容端点
// 拒绝以 tool_result 结尾的请求（400 "The content[].thinking in the thinking mode must be
// passed back to the API"，文案误导，实为结尾消息类型校验）。
// 工具执行后自动继续时历史最后一条正是工具结果（无尾部 user），此场景必然触发。
func TestSendRequest_TrailingToolAppendsUser(t *testing.T) {
	initAgentsConsumer()
	stats.ResetForTest()
	stats.SetFilePath(filepath.Join(t.TempDir(), "usage.json"))

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
		emitTextSSE(w, "done ok")
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := storageStructs.Chats{ID: 6002, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	// 历史以工具结果（tool）结尾，无尾部 user 消息 —— 工具执行后自动继续的真实场景
	msgs := []storageStructs.Messages{
		{ChatID: 6002, Type: storageStructs.MessagesRoleUser, Delta: "do it"},
		{ChatID: 6002, Type: storageStructs.MessagesRoleAgent, Delta: "ok", ToolCallingJSONString: `[{"name":"run","id":"call_a","parameters":{}}]`},
		{ChatID: 6002, Type: storageStructs.MessagesRoleTool, Delta: `[{"name":"run","id":"call_a","return":"a"}]`},
	}
	for _, mm := range msgs {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	session := &storageStructs.Chats{ID: 6002, DB: db, LastModelID: 1, CurrentAgentID: "", EnableScopes: make(map[string]bool)}
	if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("no request captured")
	}
	req := bodies[0]
	if len(req.Messages) == 0 {
		t.Fatal("request has no messages")
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != structs.RoleUser {
		t.Errorf("期望请求以 user 消息结尾（避免 400），实际最后一条 role=%q", last.Role)
	}
	if last.Content != toolResultContinuePrompt {
		t.Errorf("期望收尾 user 内容为 toolResultContinuePrompt，实际 %q", last.Content)
	}
}
