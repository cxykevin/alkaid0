package tools

import (
	"errors"
	"testing"
)

func initTestEnv() {
	ToolsList = make(map[string]*Tools)
	Scopes = make(map[string]string)
	enableScopes = make(map[string]bool)
}

func TestAddScope(t *testing.T) {
	initTestEnv()
	AddScope("scope1", "This is scope 1")
	if val, ok := Scopes["scope1"]; !ok || val != "This is scope 1" {
		t.Errorf("AddScope failed: expected 'This is scope 1', got '%v'", val)
	}
}

func TestAddTool(t *testing.T) {
	initTestEnv()
	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks:           []Hook{},
	}
	AddTool(tool)
	if _, ok := ToolsList["tool1"]; !ok {
		t.Errorf("AddTool failed: tool not found in ToolsList")
	}
	if ToolsList["tool1"].Name != "TestTool" {
		t.Errorf("AddTool failed: expected 'TestTool', got '%v'", ToolsList["tool1"].Name)
	}
}

func TestHookTool(t *testing.T) {
	initTestEnv()
	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks:           []Hook{},
	}
	AddTool(tool)

	hook := &Hook{
		Scope: "scope1",
		PreHook: func() (string, error) {
			return "preHook result", nil
		},
	}
	HookTool("tool1", hook)

	if len(ToolsList["tool1"].Hooks) != 1 {
		t.Errorf("HookTool failed: expected 1 hook, got %d", len(ToolsList["tool1"].Hooks))
	}
	if ToolsList["tool1"].Hooks[0].Scope != "scope1" {
		t.Errorf("HookTool failed: expected scope 'scope1', got '%v'", ToolsList["tool1"].Hooks[0].Scope)
	}
}

func TestEnableScope(t *testing.T) {
	initTestEnv()
	EnableScope("scope1")
	if val, ok := enableScopes["scope1"]; !ok || !val {
		t.Errorf("EnableScope failed: scope1 not enabled")
	}
}

func TestDisableScope(t *testing.T) {
	initTestEnv()
	EnableScope("scope1")
	DisableScope("scope1")
	if val, ok := enableScopes["scope1"]; !ok || val {
		t.Errorf("DisableScope failed: scope1 should be disabled")
	}
}

func TestExecToolGetPrompts(t *testing.T) {
	initTestEnv()

	AddScope("scope1", "Scope 1 prompt")
	AddScope("scope2", "Scope 2 prompt")

	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []Hook{
			{
				Scope: "scope1",
				PreHook: func() (string, error) {
					return "prehook1", nil
				},
			},
			{
				Scope: "scope2",
				PreHook: func() (string, error) {
					return "prehook2", nil
				},
			},
		},
	}
	AddTool(tool)

	// Enable only scope1
	EnableScope("scope1")

	unusedHooks, prehooks := ExecToolGetPrompts("tool1")

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

	AddScope("scope1", "Scope 1 prompt")

	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []Hook{
			{
				Scope: "invalidScope",
				PreHook: func() (string, error) {
					return "should not execute", nil
				},
			},
		},
	}
	AddTool(tool)
	EnableScope("scope1")

	unusedHooks, prehooks := ExecToolGetPrompts("tool1")

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

	AddScope("scope1", "Scope 1 prompt")

	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []Hook{
			{
				Scope: "scope1",
				OnHook: func(args map[string]any, pass []*any) (bool, []*any, error) {
					return true, pass, nil
				},
			},
		},
	}
	AddTool(tool)
	EnableScope("scope1")

	args := map[string]any{"key": "value"}
	results := ExecToolOnHook("tool1", args)

	if len(results) != 1 {
		t.Errorf("ExecToolOnHook failed: expected 1 result, got %d", len(results))
	}

	if results[0] != true {
		t.Errorf("ExecToolOnHook failed: expected true, got %v", results[0])
	}
}

func TestExecToolOnHookWithDisabledScope(t *testing.T) {
	initTestEnv()

	AddScope("scope1", "Scope 1 prompt")

	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []Hook{
			{
				Scope: "scope1",
				OnHook: func(args map[string]any, pass []*any) (bool, []*any, error) {
					return true, pass, nil
				},
			},
		},
	}
	AddTool(tool)
	// scope1 is not enabled

	args := map[string]any{"key": "value"}
	results := ExecToolOnHook("tool1", args)

	if len(results) != 0 {
		t.Errorf("ExecToolOnHook failed: expected 0 results when scope disabled, got %d", len(results))
	}
}

func TestExecToolPostHook(t *testing.T) {
	initTestEnv()

	AddScope("scope1", "Scope 1 prompt")

	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []Hook{
			{
				Scope: "scope1",
				PostHook: func(args map[string]any, passObjs []*any) (bool, []*any, map[string]any, error) {
					result := map[string]any{"status": "success"}
					return false, passObjs, result, nil
				},
			},
		},
	}
	AddTool(tool)
	EnableScope("scope1")

	args := map[string]any{"key": "value"}
	result, err := ExecToolPostHook("tool1", args)

	if err != nil {
		t.Errorf("ExecToolPostHook failed: expected no error, got %v", err)
	}

	if val, ok := result["status"]; !ok || val != "success" {
		t.Errorf("ExecToolPostHook failed: expected status 'success', got %v", val)
	}
}

func TestExecToolPostHookAllPass(t *testing.T) {
	initTestEnv()

	AddScope("scope1", "Scope 1 prompt")

	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []Hook{
			{
				Scope: "scope1",
				PostHook: func(args map[string]any, passObjs []*any) (bool, []*any, map[string]any, error) {
					return true, passObjs, nil, nil
				},
			},
		},
	}
	AddTool(tool)
	EnableScope("scope1")

	args := map[string]any{"key": "value"}
	_, err := ExecToolPostHook("tool1", args)

	if err == nil {
		t.Errorf("ExecToolPostHook failed: expected error when all hooks pass")
	}

	if err.Error() != "All tool passed" {
		t.Errorf("ExecToolPostHook failed: expected 'All tool passed' error, got %v", err)
	}
}

func TestExecToolPostHookError(t *testing.T) {
	initTestEnv()

	AddScope("scope1", "Scope 1 prompt")

	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []Hook{
			{
				Scope: "scope1",
				PostHook: func(args map[string]any, passObjs []*any) (bool, []*any, map[string]any, error) {
					return false, passObjs, nil, errors.New("hook error")
				},
			},
		},
	}
	AddTool(tool)
	EnableScope("scope1")

	args := map[string]any{"key": "value"}
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

	AddScope("scope1", "Scope 1 prompt")
	AddScope("scope2", "Scope 2 prompt")
	AddScope("scope3", "Scope 3 prompt")

	EnableScope("scope1")
	EnableScope("scope2")

	tool := &Tools{
		Name:            "TestTool",
		ID:              "tool1",
		UserDescription: "A test tool",
		Hooks: []Hook{
			{
				Scope: "scope1",
				PreHook: func() (string, error) {
					return "prehook1", nil
				},
			},
			{
				Scope: "scope2",
				PreHook: func() (string, error) {
					return "prehook2", nil
				},
			},
			{
				Scope: "scope3",
				PreHook: func() (string, error) {
					return "prehook3", nil
				},
			},
		},
	}
	AddTool(tool)

	unusedHooks, prehooks := ExecToolGetPrompts("tool1")

	// scope3 should be in unused (not enabled)
	if len(unusedHooks) != 1 {
		t.Errorf("TestMultipleScopes failed: expected 1 unused hook, got %d", len(unusedHooks))
	}

	// scope1 and scope2 should execute
	if len(prehooks) != 2 {
		t.Errorf("TestMultipleScopes failed: expected 2 prehooks, got %d", len(prehooks))
	}
}
