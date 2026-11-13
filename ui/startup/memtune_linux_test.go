//go:build linux

package startup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCgroupMemoryTargets(t *testing.T) {
	content := "12:cpu,memory:/user.slice/a\n3:cpu:/user.slice/b\n0::/user.slice/c\n"
	targets := parseCgroupMemoryTargets(content)
	if len(targets) != 2 {
		t.Fatalf("targets = %+v, want 2 entries", targets)
	}
	if want := (cgroupMemoryTarget{dir: "/user.slice/a", file: "memory/memory.limit_in_bytes"}); targets[0] != want {
		t.Fatalf("targets[0] = %+v, want %+v", targets[0], want)
	}
	if want := (cgroupMemoryTarget{dir: "/user.slice/c", file: "memory.max"}); targets[1] != want {
		t.Fatalf("targets[1] = %+v, want %+v", targets[1], want)
	}
}

func TestParseCgroupMemoryTargetsEmptyPath(t *testing.T) {
	targets := parseCgroupMemoryTargets("0::\n")
	if len(targets) != 1 || targets[0].dir != "/" {
		t.Fatalf("targets = %+v, want a single root target", targets)
	}
}

func TestReadMemoryLimitFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	cases := []struct {
		name   string
		path   string
		want   uint64
		wantOK bool
	}{
		{"valid", write("valid", "536870912\n"), 512 << 20, true},
		{"max", write("max", "max\n"), 0, false},
		{"empty", write("empty", ""), 0, false},
		{"zero", write("zero", "0\n"), 0, false},
		{"unlimited sentinel", write("sentinel", "9223372036854771712\n"), 0, false},
		{"garbage", write("garbage", "abc\n"), 0, false},
		{"missing", filepath.Join(dir, "missing"), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := readMemoryLimitFile(c.path)
			if got != c.want || ok != c.wantOK {
				t.Fatalf("readMemoryLimitFile(%q) = (%d, %v), want (%d, %v)", c.path, got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestCgroupMemoryLimitUnderWalksAncestors(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mk("user.slice/memory.max", "4294967296\n")           // 4 GiB
	mk("user.slice/app.scope/memory.max", "1073741824\n") // 1 GiB
	mk("v1/memory/memory.limit_in_bytes", "2147483648\n") // 2 GiB（cgroup v1）

	// 子目录比上级更紧：取最小值。
	if got := cgroupMemoryLimitUnder(root, "0::/user.slice/app.scope\n"); got != 1<<30 {
		t.Fatalf("limit = %d, want 1GiB (tightest ancestor)", got)
	}
	// 子目录没有限额时沿用上级限额。
	if got := cgroupMemoryLimitUnder(root, "0::/user.slice\n"); got != 4<<30 {
		t.Fatalf("limit = %d, want 4GiB", got)
	}
	// cgroup v1 的 memory 控制器路径。
	if got := cgroupMemoryLimitUnder(root, "7:memory:/v1\n"); got != 2<<30 {
		t.Fatalf("limit = %d, want 2GiB (cgroup v1)", got)
	}
	// 完全没有限额。
	if got := cgroupMemoryLimitUnder(root, "0::/\n"); got != 0 {
		t.Fatalf("limit = %d, want 0 (unlimited)", got)
	}
}

func TestCgroupMemoryLimitUnderFallsBackToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("805306368\n"), 0o644); err != nil {
		t.Fatalf("write memory.max: %v", err)
	}
	// /proc/self/cgroup 不可读（内容为空）时按挂载点根目录的限额估算，
	// 容器内 cgroup 命名空间的根目录就是该容器的层级。
	if got := cgroupMemoryLimitUnder(root, ""); got != 768<<20 {
		t.Fatalf("limit = %d, want 768MiB", got)
	}
}
