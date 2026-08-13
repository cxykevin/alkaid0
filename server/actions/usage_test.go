package actions

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/stats"
	u "github.com/cxykevin/alkaid0/utils"
)

// collectUsageBroadcasts 启动 /usage 命令并用并发安全的 channel 收集异步广播。
func collectUsageBroadcasts(t *testing.T, obj *sessionObj, arg string) []SessionUpdate {
	t.Helper()
	oldConnCall, oldSessionConn := connCallMap, sessionConnMap
	connCallMap = map[uint64]func(string, any, *string) error{}
	sessionConnMap = map[string][]uint64{}
	defer func() { connCallMap, sessionConnMap = oldConnCall, oldSessionConn }()

	sessID := cwd2SessionID(obj.cwd, obj.id)
	const connID = 100
	sessionConnMap[sessID] = []uint64{connID}

	var got []SessionUpdate
	var mu sync.Mutex
	connCallMap[connID] = func(method string, params any, _ *string) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, params.(SessionUpdate))
		return nil
	}

	if _, err := commandMaps["/usage"].Function(obj, arg); err != nil {
		t.Fatalf("/usage %q: %v", arg, err)
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]SessionUpdate(nil), got...)
}

func TestUsageCommandOutput(t *testing.T) {
	stats.SetFilePath(filepath.Join(t.TempDir(), "usage.json"))
	stats.ResetForTest()
	defer stats.ResetForTest()

	obj := &sessionObj{cwd: "/tmp/usage", id: 1}

	// 先产生一些用量
	stats.AddUsage(1, "Kimi", 100, 50, 30)

	// /usage（无参数）：应广播非空 agent_message_chunk，且带独立 cmd_ messageId
	updates := collectUsageBroadcasts(t, obj, "")
	if len(updates) == 0 {
		t.Fatal("/usage produced no broadcasts")
	}
	last := updates[len(updates)-1].Update.(SessionUpdateUpdate)
	if !strings.HasPrefix(last.MessageID, "cmd_") {
		t.Errorf("/usage messageId = %q, want cmd_ prefix", last.MessageID)
	}
	t.Logf("/usage content: %+v", last.Content)

	// /usage reset：应广播重置确认，且带独立 cmd_ messageId
	updates = collectUsageBroadcasts(t, obj, "reset")
	if len(updates) == 0 {
		t.Fatal("/usage reset produced no broadcasts")
	}
	resetLast := updates[len(updates)-1].Update.(SessionUpdateUpdate)
	if !strings.HasPrefix(resetLast.MessageID, "cmd_") {
		t.Errorf("/usage reset messageId = %q, want cmd_ prefix", resetLast.MessageID)
	}
	t.Logf("/usage reset content: %+v", resetLast.Content)

	// reset 后再 /usage：应仍广播非空内容（全 0 统计），messageId 仍独立（不合并进旧消息）
	updates = collectUsageBroadcasts(t, obj, "")
	if len(updates) == 0 {
		t.Fatal("/usage after reset produced no broadcasts")
	}
	after := updates[len(updates)-1].Update.(SessionUpdateUpdate)
	if !strings.HasPrefix(after.MessageID, "cmd_") {
		t.Errorf("/usage after reset messageId = %q, want cmd_ prefix", after.MessageID)
	}
	t.Logf("/usage after reset content: %+v", after.Content)
}

// TestUsageResetDiskFail 写盘失败时 /usage reset 仍应广播确认（内存已清零，仅附写盘失败警告），
// 避免 reset 命令静默无反馈（此前 stats.Reset() 返回 error 直接导致命令报错不广播）。
func TestUsageResetDiskFail(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}
	// usage.json 的父路径被同名文件占位 → MkdirAll 必然失败 → stats.Reset() 返回 error
	stats.SetFilePath(filepath.Join(blocker, "usage.json"))
	stats.ResetForTest()
	defer stats.ResetForTest()

	obj := &sessionObj{cwd: "/tmp/usage2", id: 2}
	updates := collectUsageBroadcasts(t, obj, "reset")
	if len(updates) == 0 {
		t.Fatal("/usage reset should broadcast even when disk write fails")
	}
	content := updates[len(updates)-1].Update.(SessionUpdateUpdate).Content
	text, _ := content.(u.H)["text"].(string)
	if !strings.Contains(text, "Token usage reset") {
		t.Fatalf("expected reset confirmation, got %q", text)
	}
	if !strings.Contains(text, "写盘失败") {
		t.Fatalf("expected disk-write warning in message, got %q", text)
	}
}
