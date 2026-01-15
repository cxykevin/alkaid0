package log

import (
	"os"
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
