package startup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureIgnoreFileCreates(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "gitignore")

	if err := ensureIgnoreFile(path); err != nil {
		t.Fatalf("ensureIgnoreFile error: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}

func TestAppendIgnoreIfMissing(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "gitignore")

	if err := os.WriteFile(path, []byte("node_modules\n"), 0644); err != nil {
		t.Fatalf("write file error: %v", err)
	}

	if err := appendIgnoreIfMissing(path); err != nil {
		t.Fatalf("appendIgnoreIfMissing error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, ".alkaid0/") {
		t.Fatalf("expected .alkaid0/ entry, got: %q", content)
	}

	if err := appendIgnoreIfMissing(path); err != nil {
		t.Fatalf("appendIgnoreIfMissing second call error: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file error: %v", err)
	}
	content = string(data)
	if strings.Count(content, ".alkaid0/") != 1 {
		t.Fatalf("expected single .alkaid0/ entry, got: %q", content)
	}
}

func TestGetGitGlobalExcludePath_DefaultPathWhenExists(t *testing.T) {
	tempDir := t.TempDir()
	xdgDir := filepath.Join(tempDir, "xdg")
	ignorePath := filepath.Join(xdgDir, "git", "ignore")

	if err := os.MkdirAll(filepath.Dir(ignorePath), 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}
	if err := os.WriteFile(ignorePath, []byte(""), 0644); err != nil {
		t.Fatalf("write file error: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	t.Setenv("PATH", "")

	path, fromConfig, err := getGitGlobalExcludePath()
	if err != nil {
		t.Fatalf("getGitGlobalExcludePath error: %v", err)
	}
	if fromConfig {
		t.Fatalf("expected fromConfig=false")
	}
	if path != ignorePath {
		t.Fatalf("expected path %q, got %q", ignorePath, path)
	}
}

func TestGetGitGlobalExcludePath_FallbackToHomeGitignore(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(homeDir, 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}

	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PATH", "")

	path, fromConfig, err := getGitGlobalExcludePath()
	if err != nil {
		t.Fatalf("getGitGlobalExcludePath error: %v", err)
	}
	if fromConfig {
		t.Fatalf("expected fromConfig=false")
	}
	if path != "~/.gitignore" {
		t.Fatalf("expected path to be ~/.gitignore, got %q", path)
	}
}

func TestGitInitMarkerPath_UsesConfigDir(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	t.Setenv("ALKAID0_CONFIG_PATH", configPath)

	markerPath, err := gitInitMarkerPath()
	if err != nil {
		t.Fatalf("gitInitMarkerPath error: %v", err)
	}
	if markerPath != filepath.Join(tempDir, "git-inited.txt") {
		t.Fatalf("unexpected marker path: %q", markerPath)
	}
}

func TestEnsureGlobalGitIgnore_SkipsWhenMarkerExists(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	markerPath := filepath.Join(tempDir, "git-inited.txt")

	if err := os.WriteFile(markerPath, []byte(""), 0644); err != nil {
		t.Fatalf("write marker error: %v", err)
	}

	t.Setenv("ALKAID0_CONFIG_PATH", configPath)
	ensureGlobalGitIgnore()
}
