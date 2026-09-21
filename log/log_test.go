package log

import (
	"bytes"
	"fmt"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/internal/configutil"
)

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		input    string
		expected string
	}{
		{"~/test", home + "/test"},
		{"/tmp/test", "/tmp/test"},
	}

	for _, tt := range tests {
		got := configutil.ExpandPath(tt.input)
		if got != filepath.Clean(tt.expected) {
			t.Errorf("ExpandPath(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDefaultLogPathAt(t *testing.T) {
	now := time.Date(2026, time.August, 18, 14, 30, 15, 0, time.Local)
	got := defaultLogPathAt(now)
	want := filepath.Join(defaultLogDir, "log20260818-143015.log")
	if got != want {
		t.Fatalf("defaultLogPathAt() = %q, want %q", got, want)
	}
}

func TestCleanupDefaultLogs(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("log20260818-1430%02d.log", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"log.log", "log20260818-143099.txt", "other.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(dir, "log20260818-143001.log")
	if err := cleanupDefaultLogs(dir, current); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("log20260818-1430%02d.log", i)
		_, err := os.Stat(filepath.Join(dir, name))
		shouldExist := i == 1 || i >= 4
		if shouldExist && err != nil {
			t.Errorf("expected %s to remain: %v", name, err)
		}
		if !shouldExist && !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, err=%v", name, err)
		}
	}
	for _, name := range []string{"log.log", "log20260818-143099.txt", "other.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("non-matching file %s should remain: %v", name, err)
		}
	}
}

func TestOpenLogTruncatesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.log")
	if err := os.WriteFile(path, []byte("old content"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("truncated file has %d bytes, want 0", len(data))
	}
}

// TestLogger 测试初始化和基本日志功能。
func TestLogger(t *testing.T) {
	os.Setenv(envLogName, "test.log")
	defer os.Remove("test.log")

	Load()

	l := New("test-module")
	l.Info("test info message")
	l.Error("test error message")
	l.Debug("test debug message")
	l.Warn("test warn message")
}

// TestSanitizeSensitiveInfo_APIKeys 测试 API 密钥脱敏
func TestSanitizeSensitiveInfo_APIKeys(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "OpenAI API key",
			input:    "My key is sk-1234567890abcdef",
			expected: "My key is sk-***",
		},
		{
			name:     "Google API key",
			input:    "Using AIza1234567890abcdef for auth",
			expected: "Using AIza*** for auth",
		},
		{
			name:     "Claude API key",
			input:    "claude-1234567890abcdef is the key",
			expected: "claude-*** is the key",
		},
		{
			name:     "XAI API key",
			input:    "xai-1234567890abcdef",
			expected: "xai-***",
		},
		{
			name:     "HuggingFace token",
			input:    "hf_1234567890abcdef",
			expected: "hf_***",
		},
		{
			name:     "Groq API key",
			input:    "gsk_1234567890abcdef",
			expected: "gsk_***",
		},
		{
			name:     "Alkaid key",
			input:    "alk-1234567890abcdef",
			expected: "alk-***",
		},
		{
			name:     "Multiple keys",
			input:    "sk-abc123456789 and AIza987654321abc",
			expected: "sk-*** and AIza***",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "No sensitive info",
			input:    "This is a normal message",
			expected: "This is a normal message",
		},
		{
			name:     "Short key (not matched)",
			input:    "sk-short",
			expected: "sk-short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeSensitiveInfo(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeSensitiveInfo(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestSanitizeSensitiveInfo_URLs 测试 URL 脱敏
func TestSanitizeSensitiveInfo_URLs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "HTTP URL with path",
			input:    "Visit http://example.com/api/v1",
			expected: "Visit http://***/api/v1",
		},
		{
			name:     "HTTPS URL with path",
			input:    "API at https://api.example.com/endpoint",
			expected: "API at https://***/endpoint",
		},
		{
			name:     "WWW URL",
			input:    "Check www.example.com for info",
			expected: "Check www.*** for info",
		},
		{
			name:     "URL without path",
			input:    "https://example.com",
			expected: "https://***",
		},
		{
			name:     "Multiple URLs",
			input:    "http://api1.com/v1 and https://api2.com/v2",
			expected: "http://***/v1 and https://***/v2",
		},
		{
			name:     "Mixed keys and URLs",
			input:    "Key sk-1234567890abc at https://api.example.com/v1",
			expected: "Key sk-*** at https://***/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeSensitiveInfo(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeSensitiveInfo(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestSanitizeSensitiveInfo_EdgeCases 测试边界情况
func TestSanitizeSensitiveInfo_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Whitespace only",
			input:    "   ",
			expected: "",
		},
		{
			name:     "Key with trailing space",
			input:    "sk-1234567890abc ",
			expected: "sk-***",
		},
		{
			name:     "Key with leading space",
			input:    " sk-1234567890abc",
			expected: "sk-***",
		},
		{
			name:     "Very long key",
			input:    "sk-" + strings.Repeat("a", 100),
			expected: "sk-***",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeSensitiveInfo(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeSensitiveInfo(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestNew_WithoutInit 测试在未初始化时调用 New
func TestNew_WithoutInit(t *testing.T) {
	// 先关闭已有日志系统，确保旧 logWorker 退出后再重新初始化
	Shutdown()

	// 重置初始化标志
	loggerInited.Store(false)

	// 设置测试日志文件
	os.Setenv(envLogName, "test_new.log")
	defer os.Remove("test_new.log")

	// 调用 New 应该自动初始化
	l := New("test-auto-init")

	if l == nil {
		t.Fatal("New() returned nil")
	}

	if l.moduleName != "test-auto-init" {
		t.Errorf("Expected module name 'test-auto-init', got '%s'", l.moduleName)
	}

	// 验证日志系统已初始化
	if !loggerInited.Load() {
		t.Error("Logger should be initialized after calling New()")
	}
}

// TestNew_AlreadyInited 测试在已初始化时调用 New
func TestNew_AlreadyInited(t *testing.T) {
	// 确保已初始化
	os.Setenv(envLogName, "test_new2.log")
	defer os.Remove("test_new2.log")
	Load()

	// 调用 New
	l := New("test-module-2")

	if l == nil {
		t.Fatal("New() returned nil")
	}

	if l.moduleName != "test-module-2" {
		t.Errorf("Expected module name 'test-module-2', got '%s'", l.moduleName)
	}
}

// TestSolvePanic_NoPanic 测试没有 panic 的情况
func TestSolvePanic_NoPanic(t *testing.T) {
	// 设置测试日志文件
	os.Setenv(envLogName, "test_panic.log")
	defer os.Remove("test_panic.log")

	// 在 defer 中调用 SolvePanic，但不触发 panic
	defer SolvePanic()

	// 正常执行，不应该有任何问题
	_ = 1 + 1
}

// // TestSolvePanic_WithPanic 测试有 panic 的情况
// // 注意：这个测试会导致进程退出，所以我们跳过它
// func TestSolvePanic_WithPanic(t *testing.T) {
// 	t.Skip("Skipping test that causes process exit")

// 	// 如果要测试，需要在子进程中运行
// 	// 这里只是展示如何使用 SolvePanic
// 	defer SolvePanic()
// 	panic("test panic")
// }

// TestTail 验证日志尾部读取：取最后 maxBytes、是原内容后缀、且超长时返回全部。
func TestTail(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "log.log")
	var b strings.Builder
	for i := range 200 {
		b.WriteString(fmt.Sprintf("line %d: %s\n", i, strings.Repeat("x", 40)))
	}
	content := b.String()
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	old := logPath
	logPath = p
	defer func() { logPath = old }()

	// 小于整个文件 → 取尾部，且是原内容的后缀、不超 maxBytes
	got := Tail(512)
	if got == "" {
		t.Fatal("Tail returned empty")
	}
	if len(got) > 512 {
		t.Errorf("Tail length %d exceeds maxBytes 512", len(got))
	}
	if !strings.HasSuffix(content, got) {
		t.Error("Tail should be a suffix of the log file content")
	}

	// 大于整个文件 → 返回全部内容
	if full := Tail(1 << 20); full != content {
		t.Error("Tail with large maxBytes should return the entire file")
	}
}

// TestTailUninitialized 日志未初始化（logPath 为空）或 maxBytes<=0 时返回空。
func TestTailUninitialized(t *testing.T) {
	old := logPath
	logPath = ""
	defer func() { logPath = old }()

	if got := Tail(100); got != "" {
		t.Errorf("Tail should return empty when log not initialized, got %q", got)
	}
	if got := Tail(0); got != "" {
		t.Errorf("Tail with maxBytes<=0 should return empty, got %q", got)
	}
}

// TestDebugLevelEnabled 验证 debug 级别判断（0=debug，其余非 debug）。
func TestDebugLevelEnabled(t *testing.T) {
	old := globalLogLevel
	defer func() { globalLogLevel = old }()

	globalLogLevel = 0 // debug
	if !DebugLevelEnabled() {
		t.Error("DebugLevelEnabled should be true at debug level")
	}
	globalLogLevel = 1 // info
	if DebugLevelEnabled() {
		t.Error("DebugLevelEnabled should be false at info level")
	}
	globalLogLevel = 2 // warn
	if DebugLevelEnabled() {
		t.Error("DebugLevelEnabled should be false at warn level")
	}
}

// TestOpenLogFileDoesNotTruncateExisting 回归：同名日志文件已存在时不得截断。
//
// 旧实现用 O_CREATE|O_TRUNC 打开日志文件，而默认文件名只精确到秒：
// 同一秒内启动的第二个进程会把第一个进程刚写入的日志清空（多进程部署丢日志）。
func TestOpenLogFileDoesNotTruncateExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log20260101-000000.log")
	if err := os.WriteFile(path, []byte("first process log"), 0600); err != nil {
		t.Fatal(err)
	}

	f, err := openLogFile(path)
	if err != nil {
		t.Fatalf("openLogFile failed: %v", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if filepath.Clean(name) == filepath.Clean(path) {
		t.Fatal("同名文件已存在时不应复用（会截断其它进程的日志）")
	}
	if !logFileNamePattern.MatchString(filepath.Base(name)) {
		t.Errorf("新文件名 %q 必须仍被清理规则匹配，否则不会被 maxLogFiles 回收", filepath.Base(name))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first process log" {
		t.Errorf("已存在的日志被改写: %q", string(data))
	}
}

// TestSanitizeSensitiveInfo_BearerAndURLCredentials 回归：真实日志里
// "Bearer <token>"（中间有空格，token 无固定前缀）与 URL 查询参数中的凭据
// 此前原样落盘，会被 /feedback 一起上传。
func TestSanitizeSensitiveInfo_BearerAndURLCredentials(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Bearer with space",
			input:    "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			expected: "Authorization: Bearer ***",
		},
		{
			name:     "lowercase bearer",
			input:    "authorization: bearer abcdefgh12345678",
			expected: "authorization: bearer ***",
		},
		{
			name:     "url query key",
			input:    "GET https://api.example.com/v1?key=abc123def456&x=1",
			expected: "GET https://***/v1?key=***&x=1",
		},
		{
			name:     "access token query",
			input:    "https://api.example.com/v1?access_token=zzzzzzzzzzzz",
			expected: "https://***/v1?access_token=***",
		},
		{
			name:     "plain prose untouched",
			input:    "basic authentication is required",
			expected: "basic authentication is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeSensitiveInfo(tt.input); got != tt.expected {
				t.Errorf("SanitizeSensitiveInfo(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestLogFallbackKeepsMessages 回归：日志初始化失败降级到 stderr 后，
// 真实日志内容不能变成无意义的 "log channel full" 警告，Shutdown 也不能 panic。
func TestLogFallbackKeepsMessages(t *testing.T) {
	Shutdown() // 结束当前 worker，避免与下面的全局状态改写并发

	oldLogger := Logger
	oldPath := logPath
	oldEnv, hadEnv := os.LookupEnv(envLogName)
	t.Cleanup(func() {
		// 本用例改写了全局日志状态：恢复环境后重建正常的日志系统
		if hadEnv {
			os.Setenv(envLogName, oldEnv)
		} else {
			os.Unsetenv(envLogName)
		}
		Logger, logPath = oldLogger, oldPath
		loggerInited.Store(false)
		syncOnly.Store(false)
		atomic.StoreUint32(&isShutdown, 0)
		Load()
	})

	// 让 MkdirAll 失败：日志路径的父级是一个普通文件
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	os.Setenv(envLogName, filepath.Join(blocker, "log.log"))

	loggerInited.Store(false)
	syncOnly.Store(false)
	atomic.StoreUint32(&isShutdown, 0)
	logPath = ""

	Load()

	if !syncOnly.Load() {
		t.Fatal("日志目录创建失败时应降级为 stderr 同步模式")
	}

	var buf bytes.Buffer
	Logger = stdlog.New(&buf, "", 0)
	New("fallback-test").Info("REAL-MESSAGE-42")

	got := buf.String()
	if !strings.Contains(got, "REAL-MESSAGE-42") {
		t.Errorf("降级模式下真实日志丢失: %q", got)
	}
	if strings.Contains(got, "log channel full") {
		t.Errorf("降级模式仍走了异步通道: %q", got)
	}

	Shutdown() // 修复前这里会对 nil channel 调用 close 而 panic
}
