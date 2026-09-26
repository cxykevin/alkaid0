package actions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	feedbacksdk "github.com/cxykevin/feederback/sdk"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/product"
	u "github.com/cxykevin/alkaid0/utils"
)

// forceFeedbackEnabled 把运行期旁路（/feedback、自动 Telemetry）的禁用判定固定为
// "未禁用"，让用例只验证提交与广播行为、不受运行环境影响。
//
// 背景：feedbackDisabled() 除 ALKAID0_DEBUG 外还读 log.DebugLevelEnabled()，而后者
// 读的是进程启动时缓存的 ALKAID0_LOG_LEVEL（首次 log.New → Load() 时确定），测试里的
// t.Setenv 改不动它。开发者 shell 常带 ALKAID0_LOG_LEVEL=debug（CI 是干净环境），
// 于是这些用例本地全挂、CI 全过。真实判定由 TestFeedbackCommandDisabledInDebug 覆盖。
func forceFeedbackEnabled(t *testing.T) {
	t.Helper()
	old := feedbackDisabledFn
	feedbackDisabledFn = func() bool { return false }
	t.Cleanup(func() { feedbackDisabledFn = old })
}

// TestFeedbackCommandEmptyArg 空参数应返回用法错误，且不等待。
func TestFeedbackCommandEmptyArg(t *testing.T) {
	forceFeedbackEnabled(t)
	obj := &sessionObj{cwd: "/tmp/fb", id: 1}
	wait, err := feedbackCommand(obj, "   ")
	if err == nil {
		t.Error("expected usage error for empty arg")
	}
	if wait {
		t.Error("expected wait=false for usage error")
	}
}

// collectFeedbackBroadcasts 启动 feedbackCommand 并用并发安全的 channel 收集异步广播。
func collectFeedbackBroadcasts(t *testing.T, obj *sessionObj, arg string) []SessionUpdate {
	t.Helper()
	oldConnCall, oldSessionConn := connCallMap, sessionConnMap
	connCallMap = map[uint64]func(string, any, *string) error{}
	sessionConnMap = map[string][]uint64{}
	defer func() { connCallMap, sessionConnMap = oldConnCall, oldSessionConn }()

	sessID := cwd2SessionID(obj.cwd, obj.id)
	const connID = 100
	sessionConnMap[sessID] = []uint64{connID}

	updates := make(chan SessionUpdate, 4)
	connCallMap[connID] = func(_ string, data any, _ *string) error {
		updates <- data.(SessionUpdate)
		return nil
	}

	wait, err := feedbackCommand(obj, arg)
	if err != nil || wait {
		t.Fatalf("handler err=%v wait=%v", err, wait)
	}

	var got []SessionUpdate
	for i := range 2 {
		select {
		case u := <-updates:
			got = append(got, u)
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for broadcast %d", i)
		}
	}
	return got
}

// TestFeedbackCommandSubmitSuccess 成功提交应广播"正在提交"与成功结果。
func TestFeedbackCommandSubmitSuccess(t *testing.T) {
	forceFeedbackEnabled(t)

	oldSubmit := feedbackSubmit
	defer func() { feedbackSubmit = oldSubmit }()
	feedbackSubmit = func(_ context.Context, content, logs, osInfo string) (*feedbacksdk.Result, error) {
		if content != "bug 描述" {
			t.Errorf("content mismatch: %q", content)
		}
		return &feedbacksdk.Result{FeedbackID: "fid-123"}, nil
	}

	got := collectFeedbackBroadcasts(t, &sessionObj{cwd: "/tmp/fb", id: 1}, "bug 描述")
	if len(got) != 2 {
		t.Fatalf("expected 2 broadcasts, got %d", len(got))
	}
	if !strings.Contains(extractBroadcastText(got[0]), "Submitting feedback") {
		t.Errorf("first broadcast should be 'submitting', got %#v", got[0])
	}
	if !strings.Contains(extractBroadcastText(got[1]), "fid-123") {
		t.Errorf("second broadcast should contain feedback id, got %#v", got[1])
	}
}

// TestFeedbackCommandSubmitFailure 失败提交应广播失败信息。
func TestFeedbackCommandSubmitFailure(t *testing.T) {
	forceFeedbackEnabled(t)

	oldSubmit := feedbackSubmit
	defer func() { feedbackSubmit = oldSubmit }()
	feedbackSubmit = func(_ context.Context, content, logs, osInfo string) (*feedbacksdk.Result, error) {
		return nil, errors.New("boom")
	}

	got := collectFeedbackBroadcasts(t, &sessionObj{cwd: "/tmp/fb", id: 1}, "bug")
	if len(got) != 2 {
		t.Fatalf("expected 2 broadcasts, got %d", len(got))
	}
	if !strings.Contains(extractBroadcastText(got[1]), "submission failed") {
		t.Errorf("second broadcast should contain failure, got %#v", got[1])
	}
}

// TestResolveFeedbackServerURL 验证反馈地址解析：config.Feedback.URL 优先，为空回退内置。
func TestResolveFeedbackServerURL(t *testing.T) {
	restore := config.GlobalConfigSwap(cfgStructs.Config{})
	defer restore()

	// config.Feedback.URL 为空 → 回退到内置 product.FeedbackServer（去除尾部斜杠）
	want := strings.TrimRight(product.FeedbackServer, "/")
	if got := resolveFeedbackServerURL(); got != want {
		t.Errorf("default URL = %q, want %q", got, want)
	}

	// config.Feedback.URL 非空 → 优先使用，且去除尾部斜杠与空白
	restore2 := config.GlobalConfigSwap(cfgStructs.Config{
		Feedback: cfgStructs.FeedbackConfig{URL: " https://custom.example.com/ "},
	})
	defer restore2()
	if got := resolveFeedbackServerURL(); got != "https://custom.example.com" {
		t.Errorf("custom URL = %q, want https://custom.example.com", got)
	}
}

// TestFeedbackCommandDisabledInDebug debug 模式下应提示禁用且不调用提交器。
func TestFeedbackCommandDisabledInDebug(t *testing.T) {
	t.Setenv("ALKAID0_DEBUG", "true")

	oldConnCall, oldSessionConn := connCallMap, sessionConnMap
	connCallMap = map[uint64]func(string, any, *string) error{}
	sessionConnMap = map[string][]uint64{}
	defer func() { connCallMap, sessionConnMap = oldConnCall, oldSessionConn }()

	cwd := "/tmp/fb"
	obj := &sessionObj{cwd: cwd, id: 1}
	sessID := cwd2SessionID(cwd, obj.id)
	const connID = 100
	sessionConnMap[sessID] = []uint64{connID}

	var got []SessionUpdate
	connCallMap[connID] = func(_ string, data any, _ *string) error {
		got = append(got, data.(SessionUpdate))
		return nil
	}

	oldSubmit := feedbackSubmit
	defer func() { feedbackSubmit = oldSubmit }()
	called := false
	feedbackSubmit = func(_ context.Context, content, logs, osInfo string) (*feedbacksdk.Result, error) {
		called = true
		return nil, nil
	}

	wait, err := feedbackCommand(obj, "bug")
	if err != nil || wait {
		t.Fatalf("handler err=%v wait=%v", err, wait)
	}
	if called {
		t.Error("submitter should not be called in debug mode")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(got))
	}
	if !strings.Contains(extractBroadcastText(got[0]), "disabled in debug") {
		t.Errorf("broadcast should mention disabled in debug, got %#v", got[0])
	}
}

// extractBroadcastText 提取 SessionUpdate 广播中的 text 内容。
func extractBroadcastText(upd SessionUpdate) string {
	inner, ok := upd.Update.(SessionUpdateUpdate)
	if !ok {
		return ""
	}
	c, ok := inner.Content.(u.H)
	if !ok {
		return ""
	}
	s, _ := c["text"].(string)
	return s
}

// TestTruncateBytes 验证字节截断与 UTF-8 边界安全。
func TestTruncateBytes(t *testing.T) {
	if got := truncateBytes("hello", 5); got != "hello" {
		t.Errorf("short string should pass through, got %q", got)
	}
	if got := truncateBytes("hello world", 5); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	// "你好世界" 共 12 字节，max=5 时只能放下 3 字节的"你"，不得切坏"好"。
	if got := truncateBytes("你好世界", 5); got != "你" {
		t.Errorf("expected rune-safe truncation '你', got %q", got)
	}
	if got := truncateBytes("你好世界", 12); got != "你好世界" {
		t.Errorf("exact fit should pass through, got %q", got)
	}
}
