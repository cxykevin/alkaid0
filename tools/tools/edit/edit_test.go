package edit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

func ptr(v any) *any { return &v }

func TestCheckPath(t *testing.T) {
	okMp := map[string]*any{"path": ptr("src/file.txt")}
	p, err := CheckPath(okMp)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if p != "src/file.txt" {
		t.Fatalf("unexpected path: %s", p)
	}

	badMp := map[string]*any{"path": ptr("../secret")}
	_, err = CheckPath(badMp)
	if err == nil {
		t.Fatalf("expected error for .. in path")
	}
}

func TestCheckTargetText(t *testing.T) {
	mp := map[string]*any{
		"target": ptr("@all"),
		"text":   ptr("hello world"),
	}
	target, text, err := CheckTargetText(mp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "@all" || text != "hello world" {
		t.Fatalf("unexpected values: %s / %s", target, text)
	}

	// missing text
	mp2 := map[string]*any{"target": ptr("x")}
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
	if out != "Hello bar bar world" {
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
	mp := map[string]*any{"path": ptr("a.txt"), "target": ptr(""), "text": ptr("hello")}
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
	mp = map[string]*any{"path": ptr("a.txt"), "target": ptr(""), "text": ptr("world")}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "a.txt"))
	if string(data) != "hello\nworld\n" {
		t.Fatalf("append2 content mismatch: %q", string(data))
	}

	// replace substring
	mp = map[string]*any{"path": ptr("b.txt"), "target": ptr("@all"), "text": ptr("foo bar")}
	_, _, ret, _ = writeFile(session, mp, nil)
	mp = map[string]*any{"path": ptr("b.txt"), "target": ptr("foo"), "text": ptr("baz")}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "b.txt"))
	s := string(data)
	if strings.TrimSuffix(s, "\n") != "baz bar" {
		t.Fatalf("replace substring mismatch: %q", s)
	}

	// replace substring on non-existent file -> expect error in ret
	mp = map[string]*any{"path": ptr("noexist.txt"), "target": ptr("x"), "text": ptr("y")}
	_, _, ret, _ = writeFile(session, mp, nil)
	if ret == nil || ret["success"] == nil {
		t.Fatalf("unexpected return map for noexist")
	}
	if ret["success"] == nil {
		t.Fatalf("unexpected return map for noexist")
	}
	if v := *ret["success"]; v == nil {
		t.Fatalf("unexpected success value nil for noexist")
	} else {
		if bv, ok := v.(bool); ok && bv {
			t.Fatalf("expected failure for replace on non-existent file")
		}
	}

	// @ln replace
	// create file with lines
	os.WriteFile(filepath.Join(tmpdir, "lines.txt"), []byte("one\ntwo\nthree\n"), 0644)
	mp = map[string]*any{"path": ptr("lines.txt"), "target": ptr("@ln:2"), "text": ptr("NEW")}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "lines.txt"))
	if string(data) != "one\nNEW\nthree\n" {
		t.Fatalf("ln replace mismatch: %q", string(data))
	}

	// @insert
	os.WriteFile(filepath.Join(tmpdir, "ins.txt"), []byte("a\nb\nc\n"), 0644)
	mp = map[string]*any{"path": ptr("ins.txt"), "target": ptr("@insert:2"), "text": ptr("X")}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "ins.txt"))
	if string(data) != "a\nX\nb\nc\n" {
		t.Fatalf("insert mismatch: %q", string(data))
	}

	// @regex
	os.WriteFile(filepath.Join(tmpdir, "rx.txt"), []byte("Hello foo FOO world"), 0644)
	mp = map[string]*any{"path": ptr("rx.txt"), "target": ptr("@regex:/foo/i"), "text": ptr("bar")}
	_, _, ret, _ = writeFile(session, mp, nil)
	data, _ = os.ReadFile(filepath.Join(tmpdir, "rx.txt"))
	if string(data) != "Hello bar bar world" {
		t.Fatalf("regex write mismatch: %q", string(data))
	}
}
