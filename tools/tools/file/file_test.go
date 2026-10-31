package file

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	_ "github.com/cxykevin/alkaid0/tools/tools/task" // 注册 @task 虚拟内容提供者
	"github.com/cxykevin/alkaid0/tools/tools/trace"
)

func init() {
	// 测试专用的虚拟对象：内容固定，src/dst 校验都靠它判断"虚拟路径"。
	trace.RegisterVirtualContent("@filetest", func(*structs.Chats) (string, bool) {
		return "virtual payload\n", true
	})
}

func newSession(t *testing.T) (*structs.Chats, string) {
	t.Helper()
	dir := t.TempDir()
	return &structs.Chats{ID: 1, Root: dir, CurrentActivatePath: "."}, dir
}

func makeArgs(kv map[string]any) map[string]*any {
	out := make(map[string]*any, len(kv))
	for k, v := range kv {
		val := any(v)
		out[k] = &val
	}
	return out
}

func run(t *testing.T, session *structs.Chats, kv map[string]any) map[string]any {
	t.Helper()
	_, _, ret, err := runFile(session, makeArgs(kv), nil)
	if err != nil {
		t.Fatalf("runFile returned error: %v", err)
	}
	out := make(map[string]any, len(ret))
	for k, v := range ret {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func wantOK(t *testing.T, res map[string]any) {
	t.Helper()
	if ok, _ := res["success"].(bool); !ok {
		t.Fatalf("expected success=true, got %#v", res)
	}
}

func wantErr(t *testing.T, res map[string]any, substr string) {
	t.Helper()
	if ok, _ := res["success"].(bool); ok {
		t.Fatalf("expected failure, got %#v", res)
	}
	msg, _ := res["error"].(string)
	if !strings.Contains(msg, substr) {
		t.Fatalf("error %q does not contain %q", msg, substr)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCopyFileCreatesParents(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "hello")

	res := run(t, s, map[string]any{"type": "copy", "src": "a.txt", "dst": "sub/dir/b.txt"})
	wantOK(t, res)
	if got := readFile(t, filepath.Join(dir, "sub", "dir", "b.txt")); got != "hello" {
		t.Fatalf("copied content = %q, want %q", got, "hello")
	}
}

func TestCopyDirectoryRecursive(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "src", "a.txt"), "A")
	writeFile(t, filepath.Join(dir, "src", "nested", "b.txt"), "B")

	res := run(t, s, map[string]any{"type": "copy", "src": "src", "dst": "out"})
	wantOK(t, res)
	if got := readFile(t, filepath.Join(dir, "out", "a.txt")); got != "A" {
		t.Fatalf("out/a.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "out", "nested", "b.txt")); got != "B" {
		t.Fatalf("out/nested/b.txt = %q", got)
	}
}

func TestCopyReportsProgress(t *testing.T) {
	s, dir := newSession(t)
	s.SetCurrentMessageID(7)

	var mu sync.Mutex
	var messages []string
	s.SetToolProgressFn(func(toolCallID, toolName, content string) {
		mu.Lock()
		messages = append(messages, toolCallID+"|"+toolName+"|"+content)
		mu.Unlock()
	})

	payload := make([]byte, progressMinBytes+1024*1024)
	writeFile(t, filepath.Join(dir, "big.bin"), string(payload))
	res := run(t, s, map[string]any{"type": "copy", "src": "big.bin", "dst": "big.copy", "_id": "tid"})
	wantOK(t, res)

	mu.Lock()
	defer mu.Unlock()
	if len(messages) == 0 {
		t.Fatal("expected at least one progress update")
	}
	if !strings.HasPrefix(messages[0], "call_1_7_tid|file|copy ") || !strings.Contains(messages[0], "%") {
		t.Fatalf("unexpected progress message: %q", messages[0])
	}
}

func TestMoveFileOverwritesDestination(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "A")
	writeFile(t, filepath.Join(dir, "b.txt"), "B")

	res := run(t, s, map[string]any{"type": "move", "src": "a.txt", "dst": "b.txt"})
	wantOK(t, res)
	if _, err := os.Lstat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "b.txt")); got != "A" {
		t.Fatalf("destination content = %q, want A", got)
	}
}

func TestMoveDirectory(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "src", "a.txt"), "A")

	res := run(t, s, map[string]any{"type": "move", "src": "src", "dst": "dst"})
	wantOK(t, res)
	if _, err := os.Lstat(filepath.Join(dir, "src")); !os.IsNotExist(err) {
		t.Fatalf("source dir still exists: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "dst", "a.txt")); got != "A" {
		t.Fatalf("dst/a.txt = %q", got)
	}
}

func TestMoveToTrashDeletesFileAndDirectory(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "old.log"), "x")
	writeFile(t, filepath.Join(dir, "olddir", "a.txt"), "y")

	res := run(t, s, map[string]any{"type": "move", "src": "old.log", "dst": "@trash"})
	wantOK(t, res)
	if _, err := os.Lstat(filepath.Join(dir, "old.log")); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}

	res = run(t, s, map[string]any{"type": "move", "src": "olddir", "dst": "@trash"})
	wantOK(t, res)
	if _, err := os.Lstat(filepath.Join(dir, "olddir")); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
}

func TestTrashOnlyForMove(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "A")

	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "a.txt", "dst": "@trash"}), `@trash is only supported when type="move"`)
	wantErr(t, run(t, s, map[string]any{"type": "link", "src": "a.txt", "dst": "@trash"}), "real file path")
}

func TestLinkCreatesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks usually requires developer mode / privileges on Windows")
	}
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "target.txt"), "T")

	res := run(t, s, map[string]any{"type": "link", "src": "target.txt", "dst": "link.txt"})
	wantOK(t, res)

	link := filepath.Join(dir, "link.txt")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("destination is not a symlink")
	}
	if got := readFile(t, link); got != "T" {
		t.Fatalf("symlink content = %q", got)
	}
	if target, _ := os.Readlink(link); target != "target.txt" {
		t.Fatalf("symlink target = %q, want relative target.txt", target)
	}

	wantErr(t, run(t, s, map[string]any{"type": "link", "src": "target.txt", "dst": "link.txt"}), "already exists")
}

func TestLinkRejectsVirtualPaths(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "A")

	wantErr(t, run(t, s, map[string]any{"type": "link", "src": "@filetest", "dst": "out.txt"}), "virtual source")
	wantErr(t, run(t, s, map[string]any{"type": "link", "src": "a.txt", "dst": "@filetest"}), "real file path")
}

func TestVirtualSourceWritesFile(t *testing.T) {
	s, dir := newSession(t)

	res := run(t, s, map[string]any{"type": "copy", "src": "@filetest", "dst": "out/v.txt"})
	wantOK(t, res)
	if got := readFile(t, filepath.Join(dir, "out", "v.txt")); got != "virtual payload\n" {
		t.Fatalf("virtual output = %q", got)
	}

	res = run(t, s, map[string]any{"type": "move", "src": "@filetest", "dst": "out/m.txt"})
	wantOK(t, res)
	if got := readFile(t, filepath.Join(dir, "out", "m.txt")); got != "virtual payload\n" {
		t.Fatalf("virtual move output = %q", got)
	}
}

func TestVirtualTaskSource(t *testing.T) {
	s, dir := newSession(t)
	s.Task = "- [ ] demo task"

	res := run(t, s, map[string]any{"type": "copy", "src": "@task", "dst": "TASK.md"})
	wantOK(t, res)
	got := readFile(t, filepath.Join(dir, "TASK.md"))
	if !strings.Contains(got, "demo task") {
		t.Fatalf("@task output does not contain the task text: %q", got)
	}
}

func TestVirtualSourceToTrashRejected(t *testing.T) {
	s, _ := newSession(t)
	wantErr(t, run(t, s, map[string]any{"type": "move", "src": "@filetest", "dst": "@trash"}), "virtual source")
}

func TestDestinationVirtualRejected(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "A")
	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "a.txt", "dst": "@filetest"}), "dst cannot be a virtual object")
}

func TestWorkspaceEscapeRejected(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "A")

	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "../a.txt", "dst": "b.txt"}), "..")
	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "a.txt", "dst": "/tmp/b.txt"}), "relative")
	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "a.txt", "dst": ".alkaid0/b.txt"}), ".alkaid0")
}

func TestCopyOntoDirectoryRejected(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "A")
	writeFile(t, filepath.Join(dir, "sub", "x.txt"), "X")

	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "a.txt", "dst": "sub"}), "destination is a directory")
}

func TestTerminalDestinationRequiresRunningJob(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "A")

	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "a.txt", "dst": "@temp/run/1"}), "run id not found")
}

func TestInvalidParameters(t *testing.T) {
	s, _ := newSession(t)
	wantErr(t, run(t, s, map[string]any{"type": "rename", "src": "a", "dst": "b"}), "unknown type")
	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "a"}), "dst is required")
	wantErr(t, run(t, s, map[string]any{"type": "move", "src": "@trash", "dst": "a"}), "src cannot be @trash")
}

func TestSamePathRejected(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "a.txt"), "A")
	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "a.txt", "dst": "a.txt"}), "same path")
	wantErr(t, run(t, s, map[string]any{"type": "move", "src": "a.txt", "dst": "./a.txt"}), "same path")
}

func TestCopyOrMoveIntoItselfRejected(t *testing.T) {
	s, dir := newSession(t)
	writeFile(t, filepath.Join(dir, "src", "a.txt"), "A")

	wantErr(t, run(t, s, map[string]any{"type": "copy", "src": "src", "dst": "src/sub"}), "inside the source")
	wantErr(t, run(t, s, map[string]any{"type": "move", "src": "src", "dst": "src/sub"}), "inside the source")
}
