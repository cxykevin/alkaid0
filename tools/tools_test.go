package tools

import (
	"errors"
	"os"
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

func initTestEnv() {
	toolobj.ToolsList = make(map[string]*toolobj.Tools)
	toolobj.Scopes = make(map[string]string)
	toolobj.EnableScopes = make(map[string]bool)

	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")
	storage.InitStorage()
}

func TestAddScope(t *testing.T) {
	initTestEnv()
	actions.AddScope("scope1", "This is scope 1")
	if val, ok := toolobj.Scopes["scope1"]; !ok || val != "This is scope 1" {
		t.Errorf("AddScope failed: expected 'This is scope 1', got '%v'", val)
	}
}

func TestAddTool(t *testing.T) {
	initTestEnv()
	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks:           []toolobj.Hook{},
	}
	actions.AddTool(tool)
	if _, ok := toolobj.ToolsList["tool1"]; !ok {
		t.Errorf("AddTool failed: tool not found in ToolsList")
	}
	if toolobj.ToolsList["tool1"].Name != "TestTool" {
		t.Errorf("AddTool failed: expected 'TestTool', got '%v'", toolobj.ToolsList["tool1"].Name)
	}
}

func TestHookTool(t *testing.T) {
	initTestEnv()
	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks:           []toolobj.Hook{},
	}
	actions.AddTool(tool)

	hook := &toolobj.Hook{
		Scope: "scope1",
		PreHook: toolobj.PreHookFunction{
			Func: func() (string, error) {
				return "preHook result", nil
			},
		},
	}
	actions.HookTool("tool1", hook)

	if len(toolobj.ToolsList["tool1"].Hooks) != 1 {
		t.Errorf("HookTool failed: expected 1 hook, got %d", len(toolobj.ToolsList["tool1"].Hooks))
	}
	if toolobj.ToolsList["tool1"].Hooks[0].Scope != "scope1" {
		t.Errorf("HookTool failed: expected scope 'scope1', got '%v'", toolobj.ToolsList["tool1"].Hooks[0].Scope)
	}
}

func TestEnableScope(t *testing.T) {
	initTestEnv()
	actions.EnableScope("scope1")
	if val, ok := toolobj.EnableScopes["scope1"]; !ok || !val {
		t.Errorf("EnableScope failed: scope1 not enabled")
	}
}

func TestDisableScope(t *testing.T) {
	initTestEnv()
	actions.EnableScope("scope1")
	actions.DisableScope("scope1")
	if val, ok := toolobj.EnableScopes["scope1"]; !ok || val {
		t.Errorf("DisableScope failed: scope1 should be disabled")
	}
}

func TestExecToolGetPrompts(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")
	actions.AddScope("scope2", "Scope 2 prompt")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "prehook1", nil
					},
				},
			},
			{
				Scope: "scope2",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "prehook2", nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)

	// Enable only scope1
	actions.EnableScope("scope1")

	unusedHooks, prehooks, _ := ExecOneToolGetPrompts("tool1")

	// scope2 should be in unused hooks
	if len(unusedHooks) != 1 {
		t.Errorf("ExecToolGetPrompts failed: expected 1 unused hook, got %d", len(unusedHooks))
	}

	// only scope1 prehook should be executed
	if len(prehooks) != 1 {
		t.Errorf("ExecToolGetPrompts failed: expected 1 prehook, got %d", len(prehooks))
	}

	if len(prehooks) > 0 && prehooks[0] != "prehook1" {
		t.Errorf("ExecToolGetPrompts failed: expected 'prehook1', got '%v'", prehooks[0])
	}
}

func TestExecToolGetPromptsWithInvalidScope(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "invalidScope",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "should not execute", nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)
	actions.EnableScope("scope1")

	unusedHooks, prehooks, _ := ExecOneToolGetPrompts("tool1")

	// No unused hooks since scope1 is enabled and no other scopes exist
	if len(unusedHooks) != 0 {
		t.Errorf("ExecToolGetPrompts failed: expected 0 unused hooks, got %d", len(unusedHooks))
	}

	// Hook with invalid scope should be skipped
	if len(prehooks) != 0 {
		t.Errorf("ExecToolGetPrompts failed: expected 0 prehooks, got %d", len(prehooks))
	}
}

func TestExecToolOnHook(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				OnHook: toolobj.OnHookFunction{
					Func: func(args map[string]*any, pass []*any) (bool, []*any, error) {
						return true, pass, nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)
	actions.EnableScope("scope1")

	v := any("value")
	args := map[string]*any{"key": &v}
	err := ExecToolOnHook("tool1", args)

	if err != nil {
		t.Errorf("ExecToolOnHook failed: expected no error, got %v", err)
	}
}

func TestExecToolOnHookWithDisabledScope(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				OnHook: toolobj.OnHookFunction{
					Func: func(args map[string]*any, pass []*any) (bool, []*any, error) {
						return true, pass, nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)
	// scope1 is not enabled

	v := any("value")
	args := map[string]*any{"key": &v}
	err := ExecToolOnHook("tool1", args)

	if err != nil {
		t.Errorf("ExecToolOnHook failed when scope disabled: expected no error, got %v", err)
	}
}

func TestExecToolPostHook(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PostHook: toolobj.PostHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, map[string]*any, error) {
						s := any("success")
						result := map[string]*any{"status": &s}
						return false, passObjs, result, nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)
	actions.EnableScope("scope1")

	v := any("value")
	args := map[string]*any{"key": &v}
	result, err := ExecToolPostHook("tool1", args)

	if err != nil {
		t.Errorf("ExecToolPostHook failed: expected no error, got %v", err)
	}

	if valPtr, ok := result["status"]; !ok {
		t.Errorf("ExecToolPostHook failed: expected status present")
	} else {
		if str, ok := (*valPtr).(string); !ok || str != "success" {
			t.Errorf("ExecToolPostHook failed: expected status 'success', got %v", *valPtr)
		}
	}
}

func TestExecToolPostHookAllPass(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PostHook: toolobj.PostHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, map[string]*any, error) {
						return true, passObjs, nil, nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)
	actions.EnableScope("scope1")

	v := any("value")
	args := map[string]*any{"key": &v}
	res, err := ExecToolPostHook("tool1", args)

	if err != nil {
		t.Errorf("ExecToolPostHook failed: expected no error when all hooks pass, got %v", err)
	}

	if len(res) != 0 {
		t.Errorf("ExecToolPostHook failed: expected empty result when all hooks pass, got %v", res)
	}
}

func TestExecToolPostHookError(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PostHook: toolobj.PostHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, map[string]*any, error) {
						return false, passObjs, nil, errors.New("hook error")
					},
				},
			},
		},
	}
	actions.AddTool(tool)
	actions.EnableScope("scope1")

	v := any("value")
	args := map[string]*any{"key": &v}
	_, err := ExecToolPostHook("tool1", args)

	if err == nil {
		t.Errorf("ExecToolPostHook failed: expected error from hook")
	}

	if err.Error() != "hook error" {
		t.Errorf("ExecToolPostHook failed: expected 'hook error', got %v", err)
	}
}

func TestMultipleScopes(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")
	actions.AddScope("scope2", "Scope 2 prompt")
	actions.AddScope("scope3", "Scope 3 prompt")

	actions.EnableScope("scope1")
	actions.EnableScope("scope2")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "prehook1", nil
					},
				},
			},
			{
				Scope: "scope2",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "prehook2", nil
					},
				},
			},
			{
				Scope: "scope3",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "prehook3", nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)

	unusedHooks, prehooks, _ := ExecOneToolGetPrompts("tool1")

	// scope3 should be in unused (not enabled)
	if len(unusedHooks) != 1 {
		t.Errorf("TestMultipleScopes failed: expected 1 unused hook, got %d", len(unusedHooks))
	}

	// scope1 and scope2 should execute
	if len(prehooks) != 2 {
		t.Errorf("TestMultipleScopes failed: expected 2 prehooks, got %d", len(prehooks))
	}
}

func TestPreHookPrioritySorting(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")
	actions.EnableScope("scope1")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PreHook: toolobj.PreHookFunction{
					Priority: 1,
					Func: func() (string, error) {
						return "low_priority", nil
					},
				},
			},
			{
				Scope: "scope1",
				PreHook: toolobj.PreHookFunction{
					Priority: 3,
					Func: func() (string, error) {
						return "highest_priority", nil
					},
				},
			},
			{
				Scope: "scope1",
				PreHook: toolobj.PreHookFunction{
					Priority: 2,
					Func: func() (string, error) {
						return "medium_priority", nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)

	_, prehooks, _ := ExecOneToolGetPrompts("tool1")

	if len(prehooks) != 3 {
		t.Errorf("TestPreHookPrioritySorting failed: expected 3 prehooks, got %d", len(prehooks))
	}

	// 应该按降序排列（高优先级在前）
	if prehooks[0] != "highest_priority" {
		t.Errorf("TestPreHookPrioritySorting failed: expected first prehook to be 'highest_priority', got '%v'", prehooks[0])
	}
	if prehooks[1] != "medium_priority" {
		t.Errorf("TestPreHookPrioritySorting failed: expected second prehook to be 'medium_priority', got '%v'", prehooks[1])
	}
	if prehooks[2] != "low_priority" {
		t.Errorf("TestPreHookPrioritySorting failed: expected third prehook to be 'low_priority', got '%v'", prehooks[2])
	}
}

func TestOnHookPrioritySorting(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")
	actions.EnableScope("scope1")

	order := make([]string, 0)

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				OnHook: toolobj.OnHookFunction{
					Priority: 1,
					Func: func(args map[string]*any, pass []*any) (bool, []*any, error) {
						order = append(order, "low")
						return true, pass, nil
					},
				},
			},
			{
				Scope: "scope1",
				OnHook: toolobj.OnHookFunction{
					Priority: 3,
					Func: func(args map[string]*any, pass []*any) (bool, []*any, error) {
						order = append(order, "high")
						return true, pass, nil
					},
				},
			},
			{
				Scope: "scope1",
				OnHook: toolobj.OnHookFunction{
					Priority: 2,
					Func: func(args map[string]*any, pass []*any) (bool, []*any, error) {
						order = append(order, "mid")
						return true, pass, nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)
	v := any("value")
	args := map[string]*any{"key": &v}
	err := ExecToolOnHook("tool1", args)

	if err != nil {
		t.Fatalf("ExecToolOnHook returned error: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("TestOnHookPrioritySorting failed: expected 3 hooks executed, got %d", len(order))
	}

	if order[0] != "high" || order[1] != "mid" || order[2] != "low" {
		t.Errorf("TestOnHookPrioritySorting failed: unexpected order %v", order)
	}
}

func TestPostHookPrioritySorting(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")
	actions.EnableScope("scope1")

	order := make([]string, 0)

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PostHook: toolobj.PostHookFunction{
					Priority: 1,
					Func: func(args map[string]*any, pass []*any) (bool, []*any, map[string]*any, error) {
						order = append(order, "low")
						return true, pass, map[string]*any{}, nil
					},
				},
			},
			{
				Scope: "scope1",
				PostHook: toolobj.PostHookFunction{
					Priority: 3,
					Func: func(args map[string]*any, pass []*any) (bool, []*any, map[string]*any, error) {
						order = append(order, "high")
						return true, pass, map[string]*any{}, nil
					},
				},
			},
			{
				Scope: "scope1",
				PostHook: toolobj.PostHookFunction{
					Priority: 2,
					Func: func(args map[string]*any, pass []*any) (bool, []*any, map[string]*any, error) {
						order = append(order, "mid")
						return true, pass, map[string]*any{}, nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)
	v := any("value")
	args := map[string]*any{"key": &v}
	_, err := ExecToolPostHook("tool1", args)

	if err != nil {
		t.Fatalf("ExecToolOnHook returned error: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("TestOnHookPrioritySorting failed: expected 3 hooks executed, got %d", len(order))
	}

	if order[0] != "high" || order[1] != "mid" || order[2] != "low" {
		t.Errorf("TestOnHookPrioritySorting failed: unexpected order %v", order)
	}
}

func TestZeroPriority(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")
	actions.EnableScope("scope1")

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PreHook: toolobj.PreHookFunction{
					Priority: 0,
					Func: func() (string, error) {
						return "zero_priority", nil
					},
				},
			},
			{
				Scope: "scope1",
				PreHook: toolobj.PreHookFunction{
					Priority: 1,
					Func: func() (string, error) {
						return "one_priority", nil
					},
				},
			},
		},
	}
	actions.AddTool(tool)

	_, prehooks, _ := ExecOneToolGetPrompts("tool1")

	if len(prehooks) != 2 {
		t.Errorf("TestZeroPriority failed: expected 2 prehooks, got %d", len(prehooks))
	}

	// 优先级为1的应该在优先级为0的前面
	if prehooks[0] != "one_priority" {
		t.Errorf("TestZeroPriority failed: expected first prehook to be 'one_priority', got '%v'", prehooks[0])
	}
	if prehooks[1] != "zero_priority" {
		t.Errorf("TestZeroPriority failed: expected second prehook to be 'zero_priority', got '%v'", prehooks[1])
	}
}

func TestExecToolGetPromptsParameters(t *testing.T) {
	initTestEnv()

	actions.AddScope("scope1", "Scope 1 prompt")
	actions.EnableScope("scope1")

	// 工具初始参数
	baseParams := map[string]parser.ToolParameters{
		"a": {Type: parser.ToolTypeString, Required: false, Description: "base a"},
		"b": {Type: parser.ToolTypeInt, Required: true, Description: "base b"},
	}

	// 钩子参数：覆盖 a，新增 c
	hookParams := map[string]parser.ToolParameters{
		"a": {Type: parser.ToolTypeString, Required: true, Description: "hook a override"},
		"c": {Type: parser.ToolTypeBoolen, Required: false, Description: "hook c"},
	}

	tool := &toolobj.Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Parameters:      baseParams,
		Hooks: []toolobj.Hook{
			{
				Scope: "scope1",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "prehook-param", nil
					},
				},
				Parameters: &hookParams,
			},
		},
	}
	actions.AddTool(tool)

	unusedHooks, prehooks, paras := ExecOneToolGetPrompts("tool1")

	if len(unusedHooks) != 0 {
		t.Errorf("TestExecToolGetPromptsParameters failed: expected 0 unused hooks, got %d", len(unusedHooks))
	}

	if len(prehooks) != 1 || prehooks[0] != "prehook-param" {
		t.Errorf("TestExecToolGetPromptsParameters failed: unexpected prehooks: %v", prehooks)
	}

	// 参数合并后应该包含 a,b,c 且 a 被钩子覆盖
	if paras == nil {
		t.Fatalf("TestExecToolGetPromptsParameters failed: paras is nil")
	}

	if val, ok := paras["a"]; !ok {
		t.Errorf("expected parameter 'a' present")
	} else if val.Description != "hook a override" || val.Required != true {
		t.Errorf("parameter 'a' was not overridden by hook: %+v", val)
	}

	if val, ok := paras["b"]; !ok {
		t.Errorf("expected parameter 'b' present")
	} else if val.Description != "base b" {
		t.Errorf("parameter 'b' was modified unexpectedly: %+v", val)
	}

	if val, ok := paras["c"]; !ok {
		t.Errorf("expected parameter 'c' present")
	} else if val.Type != parser.ToolTypeBoolen {
		t.Errorf("parameter 'c' has wrong type: %+v", val)
	}
}
