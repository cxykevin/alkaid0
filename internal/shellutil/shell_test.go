package shellutil

import (
	"runtime"
	"slices"
	"testing"
)

func TestDefault(t *testing.T) {
	if got := Default("zsh"); got != "zsh" {
		t.Errorf("Default(\"zsh\") = %q, want \"zsh\"", got)
	}
	want := "bash"
	switch runtime.GOOS {
	case "darwin":
		want = "zsh"
	case "windows":
		want = "powershell.exe"
	}
	if got := Default(""); got != want {
		t.Errorf("Default(\"\") = %q, want %q on %s", got, want, runtime.GOOS)
	}
}

func TestArgs(t *testing.T) {
	tests := []struct {
		name    string
		shell   string
		command string
		want    []string
	}{
		{"bash", "bash", "echo hi", []string{"-c", "echo hi"}},
		{"zsh", "zsh", "echo hi", []string{"-c", "echo hi"}},
		{"sh", "sh", "echo hi", []string{"-c", "echo hi"}},
		{"powershell.exe", "powershell.exe", "Get-Date", []string{"-NoProfile", "-Command", "Get-Date"}},
		{"powershell", "powershell", "Get-Date", []string{"-NoProfile", "-Command", "Get-Date"}},
		{"pwsh", "pwsh", "Get-Date", []string{"-NoProfile", "-Command", "Get-Date"}},
		{"pwsh.exe", "pwsh.exe", "Get-Date", []string{"-NoProfile", "-Command", "Get-Date"}},
		{"cmd", "cmd", "dir", []string{"/C", "dir"}},
		{"cmd.exe", "cmd.exe", "dir", []string{"/C", "dir"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Args(tt.shell, tt.command); !slices.Equal(got, tt.want) {
				t.Fatalf("Args(%q, %q) = %v, want %v", tt.shell, tt.command, got, tt.want)
			}
		})
	}
}
