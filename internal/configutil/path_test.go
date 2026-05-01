package configutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name     string
		path     string
		env      map[string]string
		expected string
	}{
		{
			name:     "home path",
			path:     "~/test",
			expected: filepath.Join(home, "test"),
		},
		{
			name:     "env var",
			path:     "$HOME/test",
			expected: filepath.Join(home, "test"),
		},
		{
			name:     "absolute path",
			path:     "/absolute/path",
			expected: "/absolute/path",
		},
		{
			name:     "relative path",
			path:     "relative/path",
			expected: "relative/path",
		},
		{
			name:     "env var with custom",
			path:     "${TEST_VAR}/file",
			env:      map[string]string{"TEST_VAR": "/custom"},
			expected: "/custom/file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			got := ExpandPath(tt.path)
			if got != tt.expected {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}
