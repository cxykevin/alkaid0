package run

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/library/json"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
)

func TestAsInt32(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected int32
		ok       bool
	}{
		{"int", 60, 60, true},
		{"float64", 60.0, 60, true},
		{"string int", "60", 60, true},
		{"string float", "60.0", 60, true},
		{"invalid string", "abc", 0, false},
		{"nil", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p *any
			if tt.input != nil {
				val := tt.input
				p = &val
			}
			got, ok := asInt32(p)
			if got != tt.expected || ok != tt.ok {
				t.Errorf("asInt32() = %v, %v; want %v, %v", got, ok, tt.expected, tt.ok)
			}
		})
	}
}

func TestAsString(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
		ok       bool
	}{
		{"string", "hello", "hello", true},
		{"StringSlot", json.StringSlot("world"), "world", true},
		{"int", 123, "", false},
		{"nil", nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p *any
			if tt.input != nil {
				val := tt.input
				p = &val
			}
			got, ok := asString(p)
			if got != tt.expected || ok != tt.ok {
				t.Errorf("asString() = %v, %v; want %v, %v", got, ok, tt.expected, tt.ok)
			}
		})
	}
}

func TestUpdateInfo(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
		ToolCallingContext:   make(map[string]any),
		ToolCallingType:      make(map[string]string),
		CurrentMessageID:     123,
	}

	mp := map[string]*any{
		"type":    func() *any { s := any("shell"); return &s }(),
		"reason":  func() *any { s := any("test reason"); return &s }(),
		"command": func() *any { s := any("echo hello"); return &s }(),
		"sandbox": func() *any { b := any(true); return &b }(),
	}

	pass, cross, err := updateInfo(session, mp, []*any{}, "test_tool")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !pass {
		t.Error("Expected pass to be true")
	}
	if cross == nil {
		t.Error("Expected cross to not be nil")
	}
}

func TestRunTaskMissingType(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	mp := map[string]*any{}

	pass, _, result, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
}

func TestRunTaskInvalidType(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	mp := map[string]*any{
		"type": func() *any { s := any("invalid"); return &s }(),
	}

	pass, _, result, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
}

func TestRunTaskMissingReason(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	mp := map[string]*any{
		"type": func() *any { s := any("shell"); return &s }(),
	}

	pass, _, result, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
}

func TestRunTaskEmptyReason(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	mp := map[string]*any{
		"type":   func() *any { s := any("shell"); return &s }(),
		"reason": func() *any { s := any(""); return &s }(),
	}

	pass, _, result, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
}

func TestRunTaskMissingCommand(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	mp := map[string]*any{
		"type":   func() *any { s := any("shell"); return &s }(),
		"reason": func() *any { s := any("test"); return &s }(),
	}

	pass, _, result, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
}

func TestRunTaskEmptyCommand(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	mp := map[string]*any{
		"type":    func() *any { s := any("shell"); return &s }(),
		"reason":  func() *any { s := any("test"); return &s }(),
		"command": func() *any { s := any(""); return &s }(),
	}

	pass, _, result, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
}

func TestRunTaskInvalidTimeout(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	mp := map[string]*any{
		"type":    func() *any { s := any("shell"); return &s }(),
		"reason":  func() *any { s := any("test"); return &s }(),
		"command": func() *any { s := any("echo hello"); return &s }(),
		"timeout": func() *any { i := any(400); return &i }(),
	}

	pass, _, result, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
}

func TestGetShell(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		expected string
	}{
		{"empty shell linux", "", "bash"},
		{"specified shell", "zsh", "zsh"},
		{"powershell", "powershell.exe", "powershell.exe"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getShell(tt.shell)
			if result != tt.expected {
				t.Errorf("getShell(%q) = %q, want %q", tt.shell, result, tt.expected)
			}
		})
	}
}

func TestGenOSInfo(t *testing.T) {
	session := &storageStructs.Chats{
		Root:                 "/tmp",
		CurrentActivatePath:  "/test",
		TemporyDataOfRequest: make(map[string]any),
	}

	result, err := genOSInfo(session)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result == "" {
		t.Error("Expected non-empty result")
	}

	// 检查结果包含工作目录信息
	if !strings.Contains(result, "/tmp/test") {
		t.Error("Expected result to contain workdir")
	}
}

func TestAsInt32EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected int32
		ok       bool
	}{
		{"int64 max", int64(2147483647), 2147483647, true},
		{"int64 min", int64(-2147483648), -2147483648, true},
		{"float64 with decimal", 3.14, 0, false},
		{"string with decimal", "3.14", 3, true},
		{"json.StringSlot int", json.StringSlot("42"), 42, true},
		{"json.StringSlot float", json.StringSlot("3.5"), 3, true},
		{"json.StringSlot invalid", json.StringSlot("abc"), 0, false},
		{"unsupported type", []int{1, 2}, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p *any
			if tt.input != nil {
				val := tt.input
				p = &val
			}
			got, ok := asInt32(p)
			if got != tt.expected || ok != tt.ok {
				t.Errorf("asInt32() = %v, %v; want %v, %v", got, ok, tt.expected, tt.ok)
			}
		})
	}
}

func TestAsStringEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
		ok       bool
	}{
		{"json.StringSlot", json.StringSlot("test"), "test", true},
		{"int", 123, "", false},
		{"bool", true, "", false},
		{"nil pointer", nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p *any
			if tt.input != nil {
				val := tt.input
				p = &val
			}
			got, ok := asString(p)
			if got != tt.expected || ok != tt.ok {
				t.Errorf("asString() = %v, %v; want %v, %v", got, ok, tt.expected, tt.ok)
			}
		})
	}
}

func TestAsInt32MoreEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected int32
		ok       bool
	}{
		{"int64 max", int64(2147483647), 2147483647, true},
		{"int64 min", int64(-2147483648), -2147483648, true},
		{"float64 with decimal", 3.14, 0, false},
		{"string with decimal", "3.14", 3, true},
		{"json.StringSlot int", json.StringSlot("42"), 42, true},
		{"json.StringSlot float", json.StringSlot("3.5"), 3, true},
		{"json.StringSlot invalid", json.StringSlot("abc"), 0, false},
		{"unsupported type", []int{1, 2}, 0, false},
		{"nil value", nil, 0, false},
		{"empty string", "", 0, false},
		{"string zero", "0", 0, true},
		{"negative string", "-123", -123, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p *any
			if tt.input != nil {
				val := tt.input
				p = &val
			}
			got, ok := asInt32(p)
			if got != tt.expected || ok != tt.ok {
				t.Errorf("asInt32() = %v, %v; want %v, %v", got, ok, tt.expected, tt.ok)
			}
		})
	}
}

func TestGetShellVarious(t *testing.T) {
	// Note: getShell depends on runtime.GOOS, so we can only test the current OS
	result := getShell("")
	if result == "" {
		t.Error("getShell should not return empty string")
	}

	result2 := getShell("zsh")
	if result2 != "zsh" {
		t.Errorf("getShell(\"zsh\") = %q, want \"zsh\"", result2)
	}
}

func TestUpdateInfoWithAllParameters(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
		ToolCallingContext:   make(map[string]any),
		ToolCallingType:      make(map[string]string),
		CurrentMessageID:     123,
	}

	mp := map[string]*any{
		"type":    func() *any { s := any("shell"); return &s }(),
		"reason":  func() *any { s := any("test reason"); return &s }(),
		"command": func() *any { s := any("echo hello"); return &s }(),
		"timeout": func() *any { i := any(30); return &i }(),
		"sandbox": func() *any { b := any(true); return &b }(),
	}

	pass, cross, err := updateInfo(session, mp, []*any{}, "test_tool")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !pass {
		t.Error("Expected pass to be true")
	}
	if cross == nil {
		t.Error("Expected cross to not be nil")
	}

	// Check if tool calling context was set
	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, "test_tool")
	if _, ok := session.ToolCallingContext[toolCallID]; !ok {
		t.Error("Expected tool calling context to be set")
	}
}

func TestUpdateInfoPartialParameters(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
		ToolCallingContext:   make(map[string]any),
		ToolCallingType:      make(map[string]string),
		CurrentMessageID:     124,
	}

	mp := map[string]*any{
		"type":   func() *any { s := any("shell"); return &s }(),
		"reason": func() *any { s := any("test reason"); return &s }(),
		// Missing command and other parameters
	}

	pass, cross, err := updateInfo(session, mp, []*any{}, "test_tool")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !pass {
		t.Error("Expected pass to be true")
	}
	if cross == nil {
		t.Error("Expected cross to not be nil")
	}

	// Check if tool calling context was set
	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, "test_tool")
	if _, ok := session.ToolCallingContext[toolCallID]; !ok {
		t.Error("Expected tool calling context to be set")
	}
}
