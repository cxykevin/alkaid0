package run

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/library/json"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/terminal/sandbox"
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
	emptyShell := ""
	switch runtime.GOOS {
	case "linux":
		emptyShell = "bash"
	case "darwin":
		emptyShell = "zsh"
	case "windows":
		emptyShell = "powershell.exe"
	default:
		emptyShell = "bash"
	}
	tests := []struct {
		name     string
		shell    string
		expected string
	}{
		{"empty shell", "", emptyShell},
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

// TestRunCmdContextCancel 测试 runCmd 在 context 取消时能正常返回（不 hang）
func TestRunCmdContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	sb, err := sandbox.New(sandbox.Config{
		IsolationMode: sandbox.IsolationNone,
	})
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	cmd, err := sb.Execute("sleep", "10")
	if err != nil {
		t.Fatalf("创建命令失败: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动命令失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer

	done := make(chan struct{})
	go func() {
		_ = runCmd(ctx, cmd, &buf, "sleep 10")
		close(done)
	}()

	// 等待命令启动后取消 context
	time.Sleep(200 * time.Millisecond)
	cancel()

	// 验证 runCmd 在 3 秒内返回（不 hang）
	select {
	case <-done:
		t.Log("runCmd 在 context 取消后正常返回")
	case <-time.After(3 * time.Second):
		t.Fatal("runCmd 在 context 取消后 3 秒仍未返回，疑似 hang")
	}
}

// TestRunCmdTimeout 测试 runCmd 在沙箱超时后能正常返回（不 hang）
func TestRunCmdTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	sb, err := sandbox.New(sandbox.Config{
		Timeout:       100 * time.Millisecond,
		IsolationMode: sandbox.IsolationNone,
	})
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	cmd, err := sb.Execute("sleep", "10")
	if err != nil {
		t.Fatalf("创建命令失败: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动命令失败: %v", err)
	}

	var buf bytes.Buffer

	done := make(chan struct{})
	go func() {
		_ = runCmd(context.Background(), cmd, &buf, "sleep 10")
		close(done)
	}()

	// 验证 runCmd 在 3 秒内返回（超时 100ms，加上缓存时间）
	select {
	case <-done:
		t.Log("runCmd 在沙箱超时后正常返回")
	case <-time.After(3 * time.Second):
		t.Fatal("runCmd 在沙箱超时后 3 秒仍未返回，疑似 hang")
	}
}

func sleepTestSession() *storageStructs.Chats {
	return &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}
}

func TestSleepTaskSuccess(t *testing.T) {
	session := sleepTestSession()
	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("test reason"); return &s }(),
		"command": func() *any { i := any(0); return &i }(),
	}

	pass, _, result, err := sleepTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || !success {
		t.Error("Expected success to be true")
	}

	if secsPtr, ok := result["seconds"]; !ok || secsPtr == nil {
		t.Fatal("Expected seconds in result")
	} else if secs, ok := (*secsPtr).(int32); !ok || secs != 0 {
		t.Errorf("Expected seconds=0, got %v", secsPtr)
	}
}

func TestSleepTaskMissingReason(t *testing.T) {
	session := sleepTestSession()
	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"command": func() *any { i := any(0); return &i }(),
	}

	pass, _, result, err := sleepTask(session, mp, []*any{})
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

func TestSleepTaskInvalidCommand(t *testing.T) {
	session := sleepTestSession()
	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("test reason"); return &s }(),
		"command": func() *any { s := any("abc"); return &s }(),
	}

	pass, _, result, err := sleepTask(session, mp, []*any{})
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

func TestSleepTaskNegativeCommand(t *testing.T) {
	session := sleepTestSession()
	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("test reason"); return &s }(),
		"command": func() *any { i := any(-1); return &i }(),
	}

	pass, _, result, err := sleepTask(session, mp, []*any{})
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

func TestSleepTaskTooLarge(t *testing.T) {
	session := sleepTestSession()
	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("test reason"); return &s }(),
		"command": func() *any { i := any(maxSleepSeconds + 1); return &i }(),
	}

	pass, _, result, err := sleepTask(session, mp, []*any{})
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

func TestSleepTaskInterrupt(t *testing.T) {
	session := sleepTestSession()
	ctx, cancel := context.WithCancel(context.Background())
	session.SetContext(ctx)
	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("test reason"); return &s }(),
		"command": func() *any { i := any(60); return &i }(),
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	pass, _, result, err := sleepTask(session, mp, []*any{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false when interrupted")
	}
	if elapsed > 3*time.Second {
		t.Errorf("sleepTask did not return promptly after cancel, elapsed=%v", elapsed)
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false when interrupted")
	}
}

func TestRunTaskSleepDispatch(t *testing.T) {
	session := sleepTestSession()
	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("wait"); return &s }(),
		"command": func() *any { i := any(0); return &i }(),
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
	} else if success, ok := (*successPtr).(bool); !ok || !success {
		t.Error("Expected success to be true")
	}
}

func TestUpdateInfoIntCommand(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
		ToolCallingContext:   make(map[string]any),
		ToolCallingType:      make(map[string]string),
		CurrentMessageID:     125,
	}

	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("wait"); return &s }(),
		"command": func() *any { i := any(30); return &i }(),
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

	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, "test_tool")
	if _, ok := session.ToolCallingContext[toolCallID]; !ok {
		t.Error("Expected tool calling context to be set")
	}
}

func TestSleepTaskStringCommand(t *testing.T) {
	session := sleepTestSession()
	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("test reason"); return &s }(),
		"command": func() *any { s := any("1"); return &s }(),
	}

	start := time.Now()
	pass, _, result, err := sleepTask(session, mp, []*any{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}

	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || !success {
		t.Error("Expected success to be true")
	}

	if secsPtr, ok := result["seconds"]; !ok || secsPtr == nil {
		t.Fatal("Expected seconds in result")
	} else if secs, ok := (*secsPtr).(int32); !ok || secs != 1 {
		t.Errorf("Expected seconds=1, got %v", secsPtr)
	}

	// 验证 string "1" 被正确转为 1 秒并实际等待
	if elapsed < 900*time.Millisecond {
		t.Errorf("Expected sleep ~1s, got %v", elapsed)
	}
}

// runOutputSession 构造带测试 DB 的会话（主路径写 trace 需要 ReferFiles/Traces 表）。
func runOutputSession(t *testing.T) *storageStructs.Chats {
	t.Helper()
	db := setupTestDB(t)
	return &storageStructs.Chats{
		DB:                   db,
		Root:                 t.TempDir(),
		CurrentActivatePath:  "",
		TemporyDataOfRequest: make(map[string]any),
	}
}

// TestRunTaskReturnsOutputField 验证 run 工具把命令输出直接放进工具结果的 output 字段，
// AI 在 role:tool 消息里第一眼就能看到本次调用结果，不必从 trace 聚合块（可能数百 KB、
// 几十条 run 记录）中大海捞针地定位最新输出。非沙盒执行保证测试确定性、不依赖 unshare。
func TestRunTaskReturnsOutputField(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}
	oldDisable := config.GlobalConfig.Agent.DisableSandbox
	config.GlobalConfig.Agent.DisableSandbox = true
	defer func() { config.GlobalConfig.Agent.DisableSandbox = oldDisable }()

	session := runOutputSession(t)
	mp := map[string]*any{
		"type":    ptrAny("shell"),
		"reason":  ptrAny("test output field"),
		"command": ptrAny("echo alkaid0-run-output-test"),
	}

	pass, _, res, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}
	outPtr, ok := res["output"]
	if !ok || outPtr == nil {
		t.Fatalf("期望结果含 output 字段，实际 %v", res)
	}
	out, ok := (*outPtr).(string)
	if !ok {
		t.Fatalf("output 应为字符串，实际 %T", *outPtr)
	}
	if !strings.Contains(out, "alkaid0-run-output-test") {
		t.Errorf("output 应包含命令输出，实际 %q", out)
	}
	// 小输出不截断，不应带截断提示
	if strings.Contains(out, "(truncated, full output at ") {
		t.Errorf("小输出不应截断，实际 %q", out)
	}
	// path 字段仍指向完整输出所在 trace 路径
	if p, ok := res["path"]; ok && p != nil {
		if ps, ok := (*p).(string); !ok || !strings.HasPrefix(ps, "@temp/run/") {
			t.Errorf("path 应为 @temp/run/ 前缀，实际 %v", *p)
		}
	}
}

// TestRunTaskOutputTruncated 验证大输出被截断到 maxRunOutputChars，并提示完整路径可读。
func TestRunTaskOutputTruncated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}
	oldDisable := config.GlobalConfig.Agent.DisableSandbox
	config.GlobalConfig.Agent.DisableSandbox = true
	defer func() { config.GlobalConfig.Agent.DisableSandbox = oldDisable }()

	session := runOutputSession(t)
	mp := map[string]*any{
		"type":    ptrAny("shell"),
		"reason":  ptrAny("test truncate"),
		"command": ptrAny("seq 1 3000"),
	}

	_, _, res, err := runTask(session, mp, []*any{})
	if err != nil {
		t.Fatalf("runTask: %v", err)
	}
	outPtr, ok := res["output"]
	if !ok || outPtr == nil {
		t.Fatalf("期望结果含 output 字段，实际 %v", res)
	}
	out, ok := (*outPtr).(string)
	if !ok {
		t.Fatalf("output 应为字符串，实际 %T", *outPtr)
	}
	if len(out) <= maxRunOutputChars {
		t.Errorf("seq 1 3000 输出应超过 %d 字符并被截断，实际 output len=%d", maxRunOutputChars, len(out))
	}
	if !strings.Contains(out, "(truncated, full output at @temp/run/") {
		t.Errorf("截断提示应指向完整路径，实际 %q", out)
	}

	// 进 trace 表的内容也应被 AddTempObject 截断（后 2000 行），不超限
	var files []storageStructs.ReferFiles
	if err := session.DB.Where("chat_id = ?", session.ID).Find(&files).Error; err != nil {
		t.Fatalf("query refer files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("期望 1 条 ReferFiles 记录，实际 %d", len(files))
	}
	if !strings.HasPrefix(files[0].Content, "(omitted)") {
		t.Errorf("trace 内容超过 2000 行时应以 (omitted) 截断，实际 %q", files[0].Content)
	}
	if ln := strings.Count(files[0].Content, "\n"); ln > 2000 {
		t.Errorf("trace 内容行数应 ≤ 2000，实际 %d", ln)
	}
}

// TestRunTaskWritesTrace 验证 run 结果全部进 trace 表（截断后）：Traces 表应有
// @temp/run/ 记录，ReferFiles 保存命令输出，AI 可经 <tracedFiles> topBlock 与
// output 字段看到本次调用结果。
func TestRunTaskWritesTrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}
	oldDisable := config.GlobalConfig.Agent.DisableSandbox
	config.GlobalConfig.Agent.DisableSandbox = true
	defer func() { config.GlobalConfig.Agent.DisableSandbox = oldDisable }()

	session := runOutputSession(t)
	mp := map[string]*any{
		"type":    ptrAny("shell"),
		"reason":  ptrAny("test trace"),
		"command": ptrAny("echo alkaid0-traced"),
	}
	if _, _, _, err := runTask(session, mp, []*any{}); err != nil {
		t.Fatalf("runTask: %v", err)
	}

	var traces []storageStructs.Traces
	if err := session.DB.Where("chat_id = ?", session.ID).Find(&traces).Error; err != nil {
		t.Fatalf("query traces: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("期望 run 结果写入 1 条 Traces，实际 %d", len(traces))
	}
	if !strings.HasPrefix(traces[0].Path, "@temp/run/") {
		t.Errorf("Traces path 应为 @temp/run/ 前缀，实际 %s", traces[0].Path)
	}

	var files []storageStructs.ReferFiles
	if err := session.DB.Where("chat_id = ?", session.ID).Find(&files).Error; err != nil {
		t.Fatalf("query refer files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("期望 1 条 ReferFiles 记录，实际 %d", len(files))
	}
	if !strings.Contains(files[0].Content, "alkaid0-traced") {
		t.Errorf("ReferFiles 应保存命令输出，实际 %q", files[0].Content)
	}
}

func TestUpdateInfoStringCommand(t *testing.T) {
	session := &storageStructs.Chats{
		TemporyDataOfRequest: make(map[string]any),
		ToolCallingContext:   make(map[string]any),
		ToolCallingType:      make(map[string]string),
		CurrentMessageID:     126,
	}

	mp := map[string]*any{
		"type":    func() *any { s := any("sleep"); return &s }(),
		"reason":  func() *any { s := any("wait"); return &s }(),
		"command": func() *any { s := any("30"); return &s }(),
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

	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, "test_tool")
	if _, ok := session.ToolCallingContext[toolCallID]; !ok {
		t.Error("Expected tool calling context to be set")
	}
}
