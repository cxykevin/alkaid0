package build

import (
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/ui/state"
)

// registerTestTool 注册一个带 OnHook 的测试工具（OnHook 纯展示：写 ToolCallingContext）。
// 返回清理函数。
func registerTestTool(t *testing.T) {
	t.Helper()
	originalScopes := toolobj.Scopes
	originalToolsList := toolobj.ToolsList
	t.Cleanup(func() {
		toolobj.Scopes = originalScopes
		toolobj.ToolsList = originalToolsList
	})

	toolobj.Scopes = map[string]string{"": "Global"}
	toolobj.ToolsList = map[string]*toolobj.Tools{
		"": {
			Name:  "Global",
			ID:    "",
			Hooks: make([]toolobj.Hook, 0),
		},
		"test_tool": {
			Scope: "",
			Name:  "test_tool",
			ID:    "test_tool",
			Hooks: []toolobj.Hook{{
				Scope: "",
				OnHook: toolobj.OnHookFunction{
					Priority: 100,
					Func: func(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
						// 模拟真实工具 OnHook：把（可能是部分的）参数写入 ToolCallingContext
						session.SetToolCalling("call_1_1_"+toolID, mp, "test_tool")
						return true, cross, nil
					},
				},
			}},
		},
	}
}

// newSolverSession 构造 ToolsSolver 测试用会话。
func newSolverSession(st state.State) *structs.Chats {
	return &structs.Chats{
		ID:                 1,
		LastModelID:        1,
		CurrentMessageID:   100,
		State:              st,
		EnableScopes:       map[string]bool{"": true},
		ToolCallingContext: make(map[string]any),
		ToolCallingType:    make(map[string]string),
	}
}

// strPtr 构造 *any 字符串指针
func strPtr(s string) *any {
	v := any(s)
	return &v
}

// TestToolsSolverStreamingOnHook 验证流式阶段（StateReciving）partial 调用也执行 OnHook：
// 增量流式恢复的关键行为——每 token 把部分参数写入 ToolCallingContext。
func TestToolsSolverStreamingOnHook(t *testing.T) {
	registerTestTool(t)
	session := newSolverSession(state.StateReciving)

	toolsDef := ToolsSolver(session, func(string, string, map[string]*any) error { return nil })
	var fn func(string, map[string]*any, bool) error
	for _, td := range *toolsDef {
		if td.Name == "test_tool" {
			fn = td.Func
		}
	}
	if fn == nil {
		t.Fatal("test_tool 未出现在 ToolsSolver 结果中")
	}

	// partial（ok=false）调用：模拟流式解析阶段工具对象尚未完整到达
	arg := map[string]*any{"path": strPtr("/tmp/a")}
	if err := fn("tid", arg, false); err != nil {
		t.Fatalf("partial 调用返回错误: %v", err)
	}

	if !session.HasToolCalling() {
		t.Fatal("流式阶段 partial 调用后 ToolCallingContext 应为非空（增量流式已恢复）")
	}
	ctx, typ, streaming := session.SnapshotToolCalling()
	if len(ctx) != 1 || typ["call_1_1_tid"] != "test_tool" {
		t.Errorf("ToolCallingContext 内容错误: ctx=%v typ=%v", ctx, typ)
	}
	// State=StateReciving 下 partial 写入应为流式增量（streaming=true）
	if !streaming["call_1_1_tid"] {
		t.Error("State=StateReciving 下 OnHook 写入应标记为流式增量（streaming=true）")
	}
}

// TestToolsSolverCompleteGated 验证 complete（ok=true）分支仍被 StateToolCalling 门控：
// 流式阶段（StateReciving）不得执行 PostHook / 触发 callback 落库。
func TestToolsSolverCompleteGated(t *testing.T) {
	registerTestTool(t)
	session := newSolverSession(state.StateReciving)

	callbackCalled := false
	toolsDef := ToolsSolver(session, func(string, string, map[string]*any) error {
		callbackCalled = true
		return nil
	})
	var fn func(string, map[string]*any, bool) error
	for _, td := range *toolsDef {
		if td.Name == "test_tool" {
			fn = td.Func
		}
	}
	if fn == nil {
		t.Fatal("test_tool 未出现在 ToolsSolver 结果中")
	}

	arg := map[string]*any{"path": strPtr("/tmp/a")}
	if err := fn("tid", arg, true); err != nil {
		t.Fatalf("complete 调用返回错误: %v", err)
	}
	if callbackCalled {
		t.Error("流式阶段 complete 调用不应触发 callback（StateToolCalling 门控应生效）")
	}
}

// TestToolsSolverCompleteInToolCalling 验证审批后（StateToolCalling）complete 调用执行 PostHook 并回调。
func TestToolsSolverCompleteInToolCalling(t *testing.T) {
	registerTestTool(t)
	session := newSolverSession(state.StateToolCalling)

	callbackCalled := false
	toolsDef := ToolsSolver(session, func(toolKey, id string, ret map[string]*any) error {
		callbackCalled = true
		return nil
	})
	var fn func(string, map[string]*any, bool) error
	for _, td := range *toolsDef {
		if td.Name == "test_tool" {
			fn = td.Func
		}
	}
	if fn == nil {
		t.Fatal("test_tool 未出现在 ToolsSolver 结果中")
	}

	arg := map[string]*any{"path": strPtr("/tmp/a")}
	if err := fn("tid", arg, true); err != nil {
		t.Fatalf("complete 调用返回错误: %v", err)
	}
	if !callbackCalled {
		t.Error("StateToolCalling 下 complete 调用应触发 callback")
	}
}
