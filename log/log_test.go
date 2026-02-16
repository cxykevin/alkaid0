package log

import (
	"os"
	"strings"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	tests := []struct {
		input    string
		expected string
	}{
		{"~/test", home + "/test"},
		{"/tmp/test", "/tmp/test"},
		{"$HOME/test", home + "/test"},
	}

	for _, tt := range tests {
		got := ExpandPath(tt.input)
		if got != tt.expected {
			t.Errorf("ExpandPath(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestLogger(t *testing.T) {
	// 测试初始化和基本日志功能
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
	// 重置初始化标志
	loggerInited = false
	
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
	if !loggerInited {
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

// TestSolvePanic_WithPanic 测试有 panic 的情况
// 注意：这个测试会导致进程退出，所以我们跳过它
func TestSolvePanic_WithPanic(t *testing.T) {
	t.Skip("Skipping test that causes process exit")
	
	// 如果要测试，需要在子进程中运行
	// 这里只是展示如何使用 SolvePanic
	defer SolvePanic()
	panic("test panic")
}
