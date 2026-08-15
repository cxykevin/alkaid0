package response_test

import (
	"encoding/json"
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/provider/response"
	"github.com/cxykevin/alkaid0/storage"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	t.Cleanup(func() { u.Unwrap(db.DB()).Close() })
	return db
}

// TestSolver_AddToken 验证普通文本和 think 内容仍由文本解析器处理。
func TestSolver_AddToken(t *testing.T) {
	db := newTestDB(t)
	session := &storageStructs.Chats{ID: 1001, DB: db}
	s := response.NewSolver(db, session)

	resp, thinking, err := s.AddToken("Hello World", "")
	if err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	_, tail, thinkingTail, err := s.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if resp+tail != "Hello World" || thinking+thinkingTail != "" {
		t.Fatalf("unexpected text result: response=%q thinking=%q", resp+tail, thinking+thinkingTail)
	}

	s = response.NewSolver(db, session)
	resp, thinking, err = s.AddToken("<think>思考内容</think>", "")
	if err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	_, tail, thinkingTail, err = s.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if resp+tail != "" || thinking+thinkingTail != "思考内容" {
		t.Fatalf("unexpected think result: response=%q thinking=%q", resp+tail, thinking+thinkingTail)
	}
}

// TestNativeSolver_ToolCalls 验证响应层把原生 tool_calls 增量交给 accumulator，
// 并保留多个 index 的顺序、参数和内部 Origin 表示。旧的 <tools> prompt-mode
// 落库/解析路径不再属于 Solver 的职责，因此不在此测试。
func TestNativeSolver_ToolCalls(t *testing.T) {
	db := newTestDB(t)
	toolobj.ToolsList = make(map[string]*toolobj.Tools)
	toolobj.Scopes = make(map[string]string)
	toolobj.Scopes["test_scope"] = "Test Scope"
	toolobj.ToolsList["test_calculator"] = &toolobj.Tools{
		Name:  "test_calculator",
		ID:    "test_calculator",
		Scope: "test_scope",
		Parameters: map[string]parser.ToolParameters{
			"expression": {Type: parser.ToolTypeString, Required: true},
		},
		Hooks: []toolobj.Hook{{
			Scope: "test_scope",
			OnHook: toolobj.OnHookFunction{Func: func(*storageStructs.Chats, map[string]*any, []*any, string) (bool, []*any, error) {
				return false, nil, nil
			}},
			PostHook: toolobj.PostHookFunction{Func: func(*storageStructs.Chats, map[string]*any, []*any) (bool, []*any, map[string]*any, error) {
				return false, nil, map[string]*any{"ok": newAny(true)}, nil
			}},
		}},
	}

	session := &storageStructs.Chats{
		ID:           2002,
		DB:           db,
		EnableScopes: map[string]bool{"test_scope": true},
	}
	s := response.NewNativeSolver(db, session)

	chunks := []structs.StreamToolCall{
		{Index: 0, ID: "call_1", Function: &structs.StreamToolCallFunc{Name: "test_calculator"}},
		{Index: 0, Function: &structs.StreamToolCallFunc{Arguments: `{"expression":"1+`}},
		{Index: 1, ID: "call_2", Function: &structs.StreamToolCallFunc{Name: "test_calculator", Arguments: `{"expression":"2+2"}`}},
		{Index: 0, Function: &structs.StreamToolCallFunc{Arguments: `1"}`}},
	}
	for _, chunk := range chunks {
		if err := s.AddNativeToolCallDelta([]structs.StreamToolCall{chunk}); err != nil {
			t.Fatalf("AddNativeToolCallDelta error: %v", err)
		}
	}

	calledTools, _, _, err := s.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if calledTools {
		t.Fatal("DoneToken should report pending native tools")
	}

	tools := s.GetTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 native tools, got %d", len(tools))
	}
	if tools[0].ID != "call_1" || tools[1].ID != "call_2" {
		t.Fatalf("unexpected tool order: %+v", tools)
	}
	if value := tools[0].Parameters["expression"]; value == nil || *value != "1+1" {
		t.Fatalf("call_1 expression = %v", value)
	}
	if value := tools[1].Parameters["expression"]; value == nil || *value != "2+2" {
		t.Fatalf("call_2 expression = %v", value)
	}

	var origin []map[string]any
	if err := json.Unmarshal([]byte(s.GetToolsOrigin()), &origin); err != nil {
		t.Fatalf("invalid native origin %q: %v", s.GetToolsOrigin(), err)
	}
	if len(origin) != 2 || origin[0]["id"] != "call_1" || origin[1]["id"] != "call_2" {
		t.Fatalf("unexpected native origin: %s", s.GetToolsOrigin())
	}
}

func newAny(v any) *any {
	return &v
}
