package u

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureWorkspacePathEmptyInputs 空参数一律拒绝。
func TestEnsureWorkspacePathEmptyInputs(t *testing.T) {
	if err := EnsureWorkspacePath("", "/tmp/target"); err == nil {
		t.Error(`EnsureWorkspacePath("", target) = nil, want error`)
	}
	if err := EnsureWorkspacePath("/tmp", ""); err == nil {
		t.Error(`EnsureWorkspacePath(root, "") = nil, want error`)
	}
}

// TestEnsureWorkspacePathInsideRoot 工作区内的已存在路径与待新建路径都应放行。
func TestEnsureWorkspacePathInsideRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cases := []struct {
		name   string
		target string
	}{
		{"existing dir", nested},
		{"new file in existing dir", filepath.Join(nested, "new.txt")},
		{"new nested path", filepath.Join(root, "x", "y", "z.txt")},
		{"dot-dot inside root", filepath.Join(root, "a", "..", "a", "b")},
		{"root itself", root},
	}
	for _, tc := range cases {
		if err := EnsureWorkspacePath(root, tc.target, ".alkaid0"); err != nil {
			t.Errorf("%s: EnsureWorkspacePath(%q) = %v, want nil", tc.name, tc.target, err)
		}
	}
}

// TestEnsureWorkspacePathDeniedComponent 受保护分量在词法层就被拦下（路径无需存在）。
func TestEnsureWorkspacePathDeniedComponent(t *testing.T) {
	root := t.TempDir()
	targets := []string{
		filepath.Join(root, ".alkaid0"),
		filepath.Join(root, ".alkaid0", "MEMORY.md"),
		filepath.Join(root, "a", ".ALKAID0", "b"),
		filepath.Join(root, "a", ".alkaid0", "b", "c.txt"),
	}
	for _, target := range targets {
		err := EnsureWorkspacePath(root, target, ".alkaid0")
		if err == nil {
			t.Errorf("EnsureWorkspacePath(%q) = nil, want denied-component error", target)
			continue
		}
		if !strings.Contains(err.Error(), ".alkaid0") {
			t.Errorf("EnsureWorkspacePath(%q) error = %v, want it to mention .alkaid0", target, err)
		}
	}

	// 未列入 denied 的同名目录应放行。
	if err := EnsureWorkspacePath(root, filepath.Join(root, ".alkaid0", "x"), ".other"); err != nil {
		t.Errorf("unlisted component rejected: %v", err)
	}
}

// TestEnsureWorkspacePathSymlinkEscape 工作区内的符号链接指向工作区外时必须拒绝。
func TestEnsureWorkspacePathSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	err := EnsureWorkspacePath(root, filepath.Join(link, "secret.txt"), ".alkaid0")
	if err == nil {
		t.Fatal("symlink escape accepted, want error")
	}
	if !strings.Contains(err.Error(), "escapes the workspace") {
		t.Errorf("error = %v, want it to mention escaping the workspace", err)
	}

	// 指向工作区外的链接本身也在词法检查之外，需走解析分支拒绝。
	if err := EnsureWorkspacePath(root, link, ".alkaid0"); err == nil {
		t.Error("bare symlink to outside accepted, want error")
	}
}

// TestEnsureWorkspacePathSymlinkToDeniedDir 工作区内链接指向受保护目录也要拒绝（解析后再查一次）。
func TestEnsureWorkspacePathSymlinkToDeniedDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	link := filepath.Join(root, "shortcut")
	if err := os.Symlink(filepath.Join(root, ".alkaid0"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	err := EnsureWorkspacePath(root, filepath.Join(link, "MEMORY.md"), ".alkaid0")
	if err == nil {
		t.Fatal("symlink into denied dir accepted, want error")
	}
	if !strings.Contains(err.Error(), ".alkaid0") {
		t.Errorf("error = %v, want it to mention .alkaid0", err)
	}
}

// TestEnsureWorkspacePathMissingRoot 工作区根不存在时回退到 Clean(root)，仍然拒绝逃逸目标。
func TestEnsureWorkspacePathMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ghost-root")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Skipf("expected %s to be missing, stat err = %v", root, err)
	}
	if err := EnsureWorkspacePath(root, filepath.Join(root, "a.txt"), ".alkaid0"); err == nil {
		t.Error("missing root accepted, want error")
	}
}

// TestEscapesRoot 相对路径是否指向 root 之外的纯前缀判断。
func TestEscapesRoot(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"..", true},
		{filepath.Join("..", "a"), true},
		{filepath.Join("..", "etc", "passwd"), true},
		{"", false},
		{".", false},
		{"a", false},
		{filepath.Join("a", "b"), false},
		{"..hidden", false},
		{filepath.Join("a", "..", "b"), false},
	}
	for _, tc := range cases {
		if got := escapesRoot(tc.rel); got != tc.want {
			t.Errorf("escapesRoot(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// TestCheckDeniedComponents 受保护分量的逐段、大小写不敏感匹配。
func TestCheckDeniedComponents(t *testing.T) {
	cases := []struct {
		name   string
		rel    string
		denied []string
		want   bool
	}{
		{"no denied list", "a/.alkaid0/b", nil, false},
		{"empty denied entry", "a/b", []string{""}, false},
		{"empty rel", "", []string{".alkaid0"}, false},
		{"dot rel", ".", []string{".alkaid0"}, false},
		{"direct match", ".alkaid0", []string{".alkaid0"}, true},
		{"nested match", "a/.alkaid0/MEMORY.md", []string{".alkaid0"}, true},
		{"case insensitive", "A/.ALKAID0/x", []string{".alkaid0"}, true},
		{"backslash separators", `a\b\.alkaid0\c`, []string{".alkaid0"}, true},
		{"prefix is not a match", "a/.alkaid0-backup/x", []string{".alkaid0"}, false},
		{"second denied entry", "x/secrets/y", []string{".alkaid0", "secrets"}, true},
	}
	for _, tc := range cases {
		err := checkDeniedComponents(tc.rel, tc.denied)
		if got := err != nil; got != tc.want {
			t.Errorf("%s: checkDeniedComponents(%q, %v) error = %v, want error == %v",
				tc.name, tc.rel, tc.denied, err, tc.want)
		}
	}
}
