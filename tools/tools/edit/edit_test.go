package edit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

//go:fix inline
func ptr(v any) *any { return new(v) }

func TestCheckPath(t *testing.T) {
	okMp := map[string]*any{"path": new(any("src/file.txt"))}
	p, err := CheckPath(okMp)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if p != "src/file.txt" {
		t.Fatalf("unexpected path: %s", p)
	}

	badMp := map[string]*any{"path": new(any("../secret"))}
	_, err = CheckPath(badMp)
	if err == nil {
		t.Fatalf("expected error for .. in path")
	}
}

func TestCheckTargetText(t *testing.T) {
	mp := map[string]*any{
		"target": new(any("@all")),
		"text":   new(any("hello world")),
	}
	target, text, err := CheckTargetText(mp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "@all" || text != "hello world" {
		t.Fatalf("unexpected values: %s / %s", target, text)
	}

	// missing text
	mp2 := map[string]*any{"target": new(any("x"))}
	_, _, err = CheckTargetText(mp2)
	if err == nil {
		t.Fatalf("expected error for missing text")
	}
}

func TestProcessStringAppendAndReplace(t *testing.T) {
	// append to existing file
	content := "line1\n"
	newc, err := ProcessString(content, "", "line2", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newc != "line1\nline2\n" {
		t.Fatalf("append produced wrong result: %q", newc)
	}

	// create new file
	newc, err = ProcessString("", "", "only", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newc != "only\n" {
		t.Fatalf("create produced wrong result: %q", newc)
	}

	// @all
	newc, err = ProcessString("old", "@all", "new", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if newc != "new\n" {
		t.Fatalf("@all produced wrong result: %q", newc)
	}
}

func TestHandleLineReplace(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	// replace single line 2
	out, err := handleLineReplace(lines, "@ln:2", "X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "a\nX\nc\nd\n"
	if out != expected {
		t.Fatalf("single replace mismatch: %q", out)
	}

	// replace range 2-3
	out, err = handleLineReplace(lines, "@ln:2-3", "Y")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected = "a\nY\nd\n"
	if out != expected {
		t.Fatalf("range replace mismatch: %q", out)
	}

	// out of range
	_, err = handleLineReplace(lines, "@ln:10", "Z")
	if err == nil {
		t.Fatalf("expected error for out of range")
	}
}

func TestHandleLineInsert(t *testing.T) {
	lines := []string{"1", "2", "3"}
	out, err := handleLineInsert(lines, "@insert:2", "X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "1\nX\n2\n3\n"
	if out != expected {
		t.Fatalf("insert mismatch: %q", out)
	}

	// insert at end
	out, err = handleLineInsert(lines, "@insert:3", "Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected = "1\n2\nZ\n3\n"
	if out != expected {
		t.Fatalf("insert at end mismatch: %q", out)
	}

	// invalid
	_, err = handleLineInsert(lines, "@insert:10", "X")
	if err == nil {
		t.Fatalf("expected error for insert out of range")
	}
}

func TestHandleRegexEdit(t *testing.T) {
	content := "Hello foo FOO world"
	// case-insensitive
	out, err := handleRegexEdit(content, "@regex:/foo/i", "bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Hello bar FOO world" {
		t.Fatalf("regex replace mismatch: %q", out)
	}

	// invalid format
	_, err = handleRegexEdit(content, "@regex:foo", "x")
	if err == nil {
		t.Fatalf("expected error for invalid regex format")
	}

	// pattern not found
	_, err = handleRegexEdit(content, "@regex:/nomatch/", "x")
	if err == nil {
		t.Fatalf("expected error for pattern not found")
	}
}

func TestWriteFile(t *testing.T) {
	tmpdir, err := os.MkdirTemp("", "edit_test")
	if err != nil {
		t.Fatalf("mktemp failed: %v", err)
	}
	defer os.RemoveAll(tmpdir)

	session := &structs.Chats{CurrentActivatePath: tmpdir}

	// create new file by append
	mp := map[string]*any{"path": new(any("a.txt")), "target": new(any("")), "text": new(any("hello"))}
	_, _, ret, err := writeFile(session, mp, nil)
	if err != nil {
		t.Fatalf("writeFile returned error: %v", err)
	}
	if ret == nil || ret["success"] == nil {
		t.Fatalf("unexpected return map")
	}
	if ret["success"] == nil {
		t.Fatalf("unexpected return map")
	}
	if v := *ret["success"]; v == nil {
		t.Fatalf("unexpected success value nil")
	} else {
		if bv, ok := v.(bool); !ok || !bv {
			t.Fatalf("expected success true, got %v", ret["error"])
		}
	}
	data, _ := os.ReadFile(filepath.Join(tmpdir, "a.txt"))
	if string(data) != "hello\n" {
		t.Fatalf("file content mismatch: %q", string(data))
	}

	// append to existing
	mp = map[string]*any{"path": new(any("a.txt")), "target": new(any("")), "text": new(any("world"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "a.txt"))
	if string(data) != "hello\nworld\n" {
		t.Fatalf("append2 content mismatch: %q", string(data))
	}

	// replace substring
	mp = map[string]*any{"path": new(any("b.txt")), "target": new(any("@all")), "text": new(any("foo bar"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	mp = map[string]*any{"path": new(any("b.txt")), "target": new(any("foo")), "text": new(any("baz"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "b.txt"))
	s := string(data)
	if strings.TrimSuffix(s, "\n") != "baz bar" {
		t.Fatalf("replace substring mismatch: %q", s)
	}

	// @ln:2 on non-existent file -> should error
	mp = map[string]*any{"path": new(any("noexist_ln2.txt")), "target": new(any("@ln:2")), "text": new(any("x"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	if ret == nil || ret["success"] == nil {
		t.Fatalf("unexpected return map for noexist_ln2")
	}
	if v := *ret["success"]; v != nil {
		if bv, ok := v.(bool); ok && bv {
			t.Fatalf("expected failure for @ln:2 on non-existent file")
		}
	}

	// replace substring on non-existent file -> should error
	mp = map[string]*any{"path": new(any("noexist.txt")), "target": new(any("x")), "text": new(any("y"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	if ret == nil || ret["success"] == nil {
		t.Fatalf("unexpected return map for noexist")
	}
	if v := *ret["success"]; v != nil {
		if bv, ok := v.(bool); ok && bv {
			t.Fatalf("expected failure for replace on non-existent file")
		}
	}

	// @regex on non-existent file -> should error
	mp = map[string]*any{"path": new(any("noexist_regex.txt")), "target": new(any("@regex:/foo/")), "text": new(any("bar"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	if ret == nil || ret["success"] == nil {
		t.Fatalf("unexpected return map for noexist_regex")
	}
	if v := *ret["success"]; v != nil {
		if bv, ok := v.(bool); ok && bv {
			t.Fatalf("expected failure for @regex on non-existent file")
		}
	}

	// @ln replace
	// create file with lines
	os.WriteFile(filepath.Join(tmpdir, "lines.txt"), []byte("one\ntwo\nthree\n"), 0644)
	mp = map[string]*any{"path": new(any("lines.txt")), "target": new(any("@ln:2")), "text": new(any("NEW"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "lines.txt"))
	if string(data) != "one\nNEW\nthree\n" {
		t.Fatalf("ln replace mismatch: %q", string(data))
	}

	// @insert
	os.WriteFile(filepath.Join(tmpdir, "ins.txt"), []byte("a\nb\nc\n"), 0644)
	mp = map[string]*any{"path": new(any("ins.txt")), "target": new(any("@insert:2")), "text": new(any("X"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "ins.txt"))
	if string(data) != "a\nX\nb\nc\n" {
		t.Fatalf("insert mismatch: %q", string(data))
	}

	// @regex
	os.WriteFile(filepath.Join(tmpdir, "rx.txt"), []byte("Hello foo FOO world"), 0644)
	mp = map[string]*any{"path": new(any("rx.txt")), "target": new(any("@regex:/foo/i")), "text": new(any("bar"))}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "rx.txt"))
	if string(data) != "Hello bar FOO world\n" {
		t.Fatalf("regex write mismatch: %q", string(data))
	}
}

func TestBuildGitPatch(t *testing.T) {
	// 修改：替换一行
	patch := buildGitPatch("/abs/file.go", "line1\nline2\nline3\n", "line1\nline2X\nline3\n", false)
	if !strings.Contains(patch, "diff --git /abs/file.go /abs/file.go") {
		t.Fatalf("patch missing diff header: %q", patch)
	}
	if !strings.Contains(patch, "--- /abs/file.go") || !strings.Contains(patch, "+++ /abs/file.go") {
		t.Fatalf("patch missing ---/+++ header: %q", patch)
	}
	if !strings.Contains(patch, "@@") {
		t.Fatalf("patch missing hunk header: %q", patch)
	}
	if !strings.Contains(patch, "-line2") || !strings.Contains(patch, "+line2X") {
		t.Fatalf("patch missing changed lines: %q", patch)
	}

	// 新建文件：旧路径为 /dev/null，operation 语义由调用方决定
	patch = buildGitPatch("/abs/new.txt", "", "hello\nworld\n", true)
	if !strings.Contains(patch, "--- /dev/null") {
		t.Fatalf("new file patch should use /dev/null: %q", patch)
	}
	if !strings.Contains(patch, "+hello") || !strings.Contains(patch, "+world") {
		t.Fatalf("new file patch missing added lines: %q", patch)
	}

	// 无变化：返回空
	if p := buildGitPatch("/abs/same.txt", "a\nb\n", "a\nb\n", false); p != "" {
		t.Fatalf("identical content should yield empty patch: %q", p)
	}
}

func TestBuildDiffContent(t *testing.T) {
	// 修改文件
	obj := buildDiffContent("/abs/main.go", "package main\n\nfunc main() {}\n", "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n", false)
	if obj == nil {
		t.Fatalf("expected diff content for modified file")
	}
	if obj["type"] != "diff" {
		t.Fatalf("unexpected type: %v", obj["type"])
	}
	changes, ok := obj["changes"].([]u.H)
	if !ok || len(changes) != 1 {
		t.Fatalf("unexpected changes: %v", obj["changes"])
	}
	if changes[0]["operation"] != "modify" || changes[0]["path"] != "/abs/main.go" || changes[0]["fileType"] != "text" {
		t.Fatalf("unexpected change entry: %v", changes[0])
	}
	patch, ok := obj["patch"].(u.H)
	if !ok || patch["format"] != "git_patch" {
		t.Fatalf("unexpected patch: %v", obj["patch"])
	}

	// 新建文件：operation = add
	obj = buildDiffContent("/abs/new.go", "", "package main\n", true)
	changes, ok = obj["changes"].([]u.H)
	if !ok || len(changes) != 1 {
		t.Fatalf("unexpected add changes: %v", obj["changes"])
	}
	if changes[0]["operation"] != "add" {
		t.Fatalf("expected add operation, got %v", changes[0]["operation"])
	}

	// 无变化：返回 nil
	if obj := buildDiffContent("/abs/same.go", "a\n", "a\n", false); obj != nil {
		t.Fatalf("identical content should yield nil diff: %v", obj)
	}
}

func TestLineDiff(t *testing.T) {
	ops := lineDiff([]string{"a", "b", "c"}, []string{"a", "x", "c"})
	var out []string
	for _, op := range ops {
		out = append(out, string(op.kind)+op.text)
	}
	got := strings.Join(out, "|")
	// 期望：保留 a、删除 b、新增 x、保留 c（LCS 回溯顺序）
	if got != " a| b|-b|+x| c" && got != " a|-b|+x| c" {
		t.Fatalf("unexpected line diff: %q", got)
	}

	// 退化路径：超大行数乘积直接整体替换
	bigOld := make([]string, 3000)
	bigNew := make([]string, 3000)
	for i := range bigOld {
		bigOld[i] = "old"
		bigNew[i] = "new"
	}
	ops = lineDiff(bigOld, bigNew)
	if len(ops) != 6000 {
		t.Fatalf("degraded diff should replace all lines, got %d ops", len(ops))
	}
}
