package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidMountName(t *testing.T) {
	cases := map[string]bool{
		"data":            true,
		"Data_1":          true,
		"_x9":             true,
		"":                false,
		"a-b":             false,
		"a.b":             false,
		"../x":            false,
		"a/b":             false,
		"中文":              false,
		"name with space": false,
	}
	for name, want := range cases {
		if got := ValidMountName(name); got != want {
			t.Fatalf("ValidMountName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMountTableRoundTrip(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := MountTargets(ws); len(got) != 0 {
		t.Fatalf("MountTargets on empty workspace = %v, want none", got)
	}

	if err := AddMount(ws, "data", target); err != nil {
		t.Fatalf("AddMount failed: %v", err)
	}
	entries := LoadMounts(ws)
	if len(entries) != 1 || entries[0].Name != "data" || entries[0].Target != canonicalPath(target) {
		t.Fatalf("LoadMounts = %#v, want one data -> %s", entries, canonicalPath(target))
	}
	targets := MountTargets(ws)
	if len(targets) != 1 || targets[0] != canonicalPath(target) {
		t.Fatalf("MountTargets = %v, want [%s]", targets, canonicalPath(target))
	}

	// 同名挂载更新目标（重新挂载同一名字），表里仍只有一条。
	other := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := AddMount(ws, "data", other); err != nil {
		t.Fatalf("AddMount replace failed: %v", err)
	}
	entries = LoadMounts(ws)
	if len(entries) != 1 || entries[0].Target != canonicalPath(other) {
		t.Fatalf("LoadMounts after replace = %#v, want one data -> %s", entries, canonicalPath(other))
	}

	// 目标被删除后不再进入可写白名单。
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	if got := MountTargets(ws); len(got) != 0 {
		t.Fatalf("MountTargets after target removed = %v, want none", got)
	}
}

func TestAddMountRejectsInvalid(t *testing.T) {
	ws := t.TempDir()
	dir := t.TempDir()
	file := filepath.Join(ws, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		target string
	}{
		{"bad-name", dir},
		{"data", "relative/path"},
		{"data", filepath.Join(ws, "missing")},
		{"data", file},
	}
	for _, c := range cases {
		if err := AddMount(ws, c.name, c.target); err == nil {
			t.Fatalf("AddMount(%q, %q) should fail", c.name, c.target)
		}
	}
	if entries := LoadMounts(ws); len(entries) != 0 {
		t.Fatalf("invalid AddMount must not write the table, got %#v", entries)
	}
}

// TestSandboxGrantsMountTargets 验证挂载表里的目标目录会被并入沙盒可写白名单。
// 工作区放在家目录下：系统临时目录本身就在可写白名单里，用它无法区分策略是否生效。
func TestSandboxGrantsMountTargets(t *testing.T) {
	root := policyTestRoot(t)
	ws := filepath.Join(root, "ws")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{ws, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Config{WorkDir: ws, Workspace: ws, IsolationMode: IsolationOS}
	before, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	probe := filepath.Join(outside, "probe.txt")
	if before.IsPathWritable(probe) {
		t.Fatalf("outside directory must be read-only before mounting")
	}

	if err := AddMount(ws, "outside", outside); err != nil {
		t.Fatalf("AddMount failed: %v", err)
	}
	after, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if !after.IsPathWritable(probe) {
		t.Fatalf("mounted target %s must be writable inside the sandbox", outside)
	}
	if !slicesContain(after.GetWritableDirs(), canonicalPath(outside)) {
		t.Fatalf("GetWritableDirs = %v, want %s", after.GetWritableDirs(), canonicalPath(outside))
	}
}

func TestMountNameFromLink(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"data.alkmount", "data", true},
		{"dir/m_1.alkmount", "m_1", true},
		{"data.alkmount.bak", "", false},
		{"data", "", false},
		{"a-b.alkmount", "", false},
		{"数据.alkmount", "", false},
	}
	for _, c := range cases {
		got, ok := MountNameFromLink(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("MountNameFromLink(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestRemoveMount(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := AddMount(ws, "data", target); err != nil {
		t.Fatalf("AddMount failed: %v", err)
	}
	if !RemoveMount(ws, "data") {
		t.Fatal("RemoveMount should report true for an existing entry")
	}
	if entries := LoadMounts(ws); len(entries) != 0 {
		t.Fatalf("LoadMounts after remove = %#v, want empty", entries)
	}
	if RemoveMount(ws, "data") {
		t.Fatal("second RemoveMount should report false")
	}
}

// TestReconcileMounts 验证自愈只清理失效记录，且不会登记未知链接。
func TestReconcileMounts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks usually requires developer mode / privileges on Windows")
	}
	ws := t.TempDir()
	target := filepath.Join(t.TempDir(), "good")
	other := filepath.Join(t.TempDir(), "other")
	broken := filepath.Join(t.TempDir(), "broken")
	for _, dir := range []string{target, other, broken} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 有效挂载：保留
	if err := AddMount(ws, "good", target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, MountLinkPath(ws, "good")); err != nil {
		t.Fatal(err)
	}
	// 链接缺失
	if err := AddMount(ws, "missing", target); err != nil {
		t.Fatal(err)
	}
	// 链接指向错误
	if err := AddMount(ws, "wrong", target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, MountLinkPath(ws, "wrong")); err != nil {
		t.Fatal(err)
	}
	// 目标目录不可达
	if err := AddMount(ws, "gone", broken); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(broken, MountLinkPath(ws, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(broken); err != nil {
		t.Fatal(err)
	}
	// 表里没有的链接：不登记、也不删除
	if err := os.Symlink(target, MountLinkPath(ws, "unknown")); err != nil {
		t.Fatal(err)
	}

	if removed := ReconcileMounts(ws); removed != 3 {
		t.Fatalf("ReconcileMounts removed = %d, want 3", removed)
	}
	entries := LoadMounts(ws)
	if len(entries) != 1 || entries[0].Name != "good" {
		t.Fatalf("LoadMounts after reconcile = %#v, want only good", entries)
	}
	if _, err := os.Lstat(MountLinkPath(ws, "wrong")); !os.IsNotExist(err) {
		t.Fatalf("stale link must be removed: %v", err)
	}
	if _, err := os.Lstat(MountLinkPath(ws, "unknown")); err != nil {
		t.Fatalf("unknown link must be left alone: %v", err)
	}
	if removed := ReconcileMounts(ws); removed != 0 {
		t.Fatalf("second reconcile removed = %d, want 0", removed)
	}
}

func slicesContain(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
