package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitignore(t *testing.T) {
	tests := []struct {
		name    string
		content string
		path    string
		isDir   bool
		want    bool
	}{
		{
			name:    "match filename",
			content: "*.log\nnode_modules/",
			path:    "test.log",
			isDir:   false,
			want:    true,
		},
		{
			name:    "match directory only",
			content: "node_modules/",
			path:    "node_modules",
			isDir:   true,
			want:    true,
		},
		{
			name:    "not match file when dirOnly",
			content: "node_modules/",
			path:    "node_modules",
			isDir:   false,
			want:    false,
		},
		{
			name:    "negate pattern",
			content: "*.log\n!important.log",
			path:    "important.log",
			isDir:   false,
			want:    false,
		},
		{
			name:    "match nested dir",
			content: "build/",
			path:    "out/build",
			isDir:   true,
			want:    true,
		},
		{
			name:    "nested path not match when rooted",
			content: "/build/",
			path:    "out/build",
			isDir:   true,
			want:    false,
		},
		{
			name:    "comment and empty lines",
			content: "# comment\n\n*.tmp\n",
			path:    "file.tmp",
			isDir:   false,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns := parseGitignore(tt.content)
			got := matchGitignore(patterns, tt.path, tt.isDir)
			if got != tt.want {
				t.Errorf("matchGitignore(%q, %q, %v) = %v, want %v", tt.content, tt.path, tt.isDir, got, tt.want)
			}
		})
	}
}

func TestLoadGitignore(t *testing.T) {
	dir := t.TempDir()
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("*.log\n*.tmp\n"), 0644); err != nil {
		t.Fatal(err)
	}

	patterns := loadGitignore(dir)
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(patterns))
	}

	if !matchGitignore(patterns, "test.log", false) {
		t.Error("expected test.log to be ignored")
	}
	if !matchGitignore(patterns, "test.tmp", false) {
		t.Error("expected test.tmp to be ignored")
	}
	if matchGitignore(patterns, "test.go", false) {
		t.Error("expected test.go to NOT be ignored")
	}
}

func TestLoadGitignoreNoFile(t *testing.T) {
	dir := t.TempDir()
	patterns := loadGitignore(dir)
	if patterns != nil {
		t.Errorf("expected nil for missing .gitignore, got %d patterns", len(patterns))
	}
}

func TestGrepFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "line one\nline two with query\nline three\nquery at end"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	results := grepFile(filePath, "test.txt", "query", nil, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Line != 2 {
		t.Errorf("expected line 2, got %d", results[0].Line)
	}
	if results[1].Line != 4 {
		t.Errorf("expected line 4, got %d", results[1].Line)
	}
}

func TestGrepFileMaxResults(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "query line\nquery line 2\nquery line 3"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	results := grepFile(filePath, "test.txt", "query", nil, 1)
	if len(results) != 1 {
		t.Fatalf("expected 1 result (limited), got %d", len(results))
	}
}

func TestAiGrep(t *testing.T) {
	dir := t.TempDir()

	// 创建一些文件
	files := map[string]string{
		"main.go":           "package main\nfunc main() {\n\tprintln(\"hello\")\n}",
		"utils.go":          "package main\nfunc helper() {\n\tprintln(\"hello world\")\n}",
		"test.log":          "this is a log file\n",
		"README.md":         "# Project\nHello world\n",
		"node_modules/a.js": "ignored\n",
	}

	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 搜索 "hello"
	results := aiGrep(context.Background(), dir, "hello", false, true, nil, 10)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'hello'")
	}

	found := false
	for _, r := range results {
		if r.FilePath == "main.go" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected main.go to be in results")
	}
}

func TestAiGrepWithGitignore(t *testing.T) {
	dir := t.TempDir()

	// 创建 .gitignore
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"main.go":   "package main\nfunc hello() {}\n",
		"debug.log": "hello from log\n",
	}

	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// includeGitignored=false 时，debug.log 应被忽略
	results := aiGrep(context.Background(), dir, "hello", false, true, nil, 10)
	for _, r := range results {
		if r.FilePath == "debug.log" {
			t.Error("debug.log should be ignored by gitignore")
		}
	}

	// includeGitignored=true 时，debug.log 应被搜到
	results = aiGrep(context.Background(), dir, "hello", true, true, nil, 10)
	foundLog := false
	for _, r := range results {
		if r.FilePath == "debug.log" {
			foundLog = true
			break
		}
	}
	if !foundLog {
		t.Error("debug.log should be found with includeGitignored=true")
	}
}

func TestAiGrepBlacklist(t *testing.T) {
	dir := t.TempDir()

	// 创建黑名单目录中的文件
	for _, name := range []string{".alkaid0", ".git", ".env"} {
		fullPath := filepath.Join(dir, name, "secret.txt")
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("query result here\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 创建正常文件
	normalPath := filepath.Join(dir, "normal.txt")
	if err := os.WriteFile(normalPath, []byte("query result here\n"), 0644); err != nil {
		t.Fatal(err)
	}

	results := aiGrep(context.Background(), dir, "query", false, true, nil, 10)
	for _, r := range results {
		if r.FilePath == ".alkaid0/secret.txt" || r.FilePath == ".git/secret.txt" || r.FilePath == ".env/secret.txt" {
			t.Errorf("blacklisted path should not appear: %s", r.FilePath)
		}
	}
}

func TestAiGrepBinaryExts(t *testing.T) {
	dir := t.TempDir()

	binaryPath := filepath.Join(dir, "image.png")
	if err := os.WriteFile(binaryPath, []byte("this is png content with query\n"), 0644); err != nil {
		t.Fatal(err)
	}

	results := aiGrep(context.Background(), dir, "query", false, true, nil, 10)
	for _, r := range results {
		if r.FilePath == "image.png" {
			t.Error("binary file .png should be skipped")
		}
	}
}

func TestFormatResults(t *testing.T) {
	results := []searchResult{
		{Source: "grep", FilePath: "main.go", Line: 5, Content: "func hello()"},
		{Source: "context", FilePath: "utils.go", Symbol: "Helper", Content: "Helper function", Score: 0.85},
	}

	output := formatResults(results)
	if !strings.Contains(output, "grep") || !strings.Contains(output, "context") {
		t.Error("formatResults should include source markers")
	}
	if !strings.Contains(output, "main.go") || !strings.Contains(output, "utils.go") {
		t.Error("formatResults should include file paths")
	}
}

func TestFormatResultsEmpty(t *testing.T) {
	output := formatResults([]searchResult{})
	if output != "No results found." {
		t.Errorf("expected 'No results found.', got %q", output)
	}
}
