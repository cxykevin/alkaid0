package outline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestFile 在临时工作目录里写一个文件并按 slash 相对路径返回。
func writeTestFile(t *testing.T, dir, rel, content string) string {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return abs
}

// TestPutGetRoundTrip 大纲按 mtime/size 命中，正文按起始行排序并携带生成时间。
func TestPutGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	defer CloseAll()
	rel := "pkg/big.go"
	writeTestFile(t, dir, rel, "package pkg\n")

	syms := []Symbol{
		{Name: "B", Kind: "function", Signature: "func B(x int) int", Line: 20},
		{Name: "A", Kind: "type", Signature: "type A struct", Line: 3},
	}
	if _, ok := Put(dir, rel, syms); !ok {
		t.Fatal("Put 应成功")
	}

	rec, ok := Get(dir, rel)
	if !ok {
		t.Fatal("Get 应命中未过期大纲")
	}
	if rec.BuiltAt.IsZero() || rec.FileMTime.IsZero() {
		t.Errorf("应记录生成时间与文件 mtime, got built=%v mtime=%v", rec.BuiltAt, rec.FileMTime)
	}
	if !strings.Contains(rec.Text, "L3\ttype\ttype A struct") {
		t.Errorf("大纲缺少类型符号, got:\n%s", rec.Text)
	}
	if !strings.Contains(rec.Text, "L20\tfunction\tfunc B(x int) int") {
		t.Errorf("大纲缺少函数符号, got:\n%s", rec.Text)
	}
	if strings.Index(rec.Text, "L3") > strings.Index(rec.Text, "L20") {
		t.Errorf("大纲应按起始行排序, got:\n%s", rec.Text)
	}
	if !Cached(dir, rel) {
		t.Error("Cached 应与 Get 一致")
	}
}

// TestGetExpiresOnMtimeChange 文件被改后 mtime 变化 → 缓存视为过期。
func TestGetExpiresOnMtimeChange(t *testing.T) {
	dir := t.TempDir()
	defer CloseAll()
	rel := "a.go"
	abs := writeTestFile(t, dir, rel, "package a\n")
	if _, ok := Put(dir, rel, []Symbol{{Name: "A", Line: 1}}); !ok {
		t.Fatal("Put 应成功")
	}

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(abs, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, ok := Get(dir, rel); ok {
		t.Fatal("mtime 变化后应判定过期")
	}
	if Cached(dir, rel) {
		t.Fatal("Cached 也应判定过期")
	}
}

// TestGetExpiresOnSizeChange 同一 mtime 下内容长度变化（原地改写）也要判过期。
func TestGetExpiresOnSizeChange(t *testing.T) {
	dir := t.TempDir()
	defer CloseAll()
	rel := "b.go"
	abs := writeTestFile(t, dir, rel, "package b\n")
	if _, ok := Put(dir, rel, []Symbol{{Name: "B", Line: 1}}); !ok {
		t.Fatal("Put 应成功")
	}
	st, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := os.WriteFile(abs, []byte("package bb\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	// 把 mtime 还原成记录时的值，只让 size 变化
	if err := os.Chtimes(abs, st.ModTime(), st.ModTime()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, ok := Get(dir, rel); ok {
		t.Fatal("size 变化后应判定过期")
	}
}

// TestDropAndEmptySymbols 空符号/显式 Drop 都会删除记录，避免留下过期大纲。
func TestDropAndEmptySymbols(t *testing.T) {
	dir := t.TempDir()
	defer CloseAll()
	rel := "c.go"
	writeTestFile(t, dir, rel, "package c\n")

	if _, ok := Put(dir, rel, []Symbol{{Name: "C", Line: 1}}); !ok {
		t.Fatal("Put 应成功")
	}
	if _, ok := Put(dir, rel, nil); ok {
		t.Fatal("空符号时 Put 应返回 false")
	}
	if _, ok := Get(dir, rel); ok {
		t.Fatal("空符号应删除旧大纲")
	}

	if _, ok := Put(dir, rel, []Symbol{{Name: "C", Line: 1}}); !ok {
		t.Fatal("Put 应成功")
	}
	if err := Drop(dir, rel); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if _, ok := Get(dir, rel); ok {
		t.Fatal("Drop 后不应命中")
	}

	// 文件不存在时 Put 不应留下记录
	if _, ok := Put(dir, "missing.go", []Symbol{{Name: "M", Line: 1}}); ok {
		t.Fatal("文件不存在时 Put 应返回 false")
	}
}

// TestRenderCapsSymbols 超过上限只渲染 MaxSymbols 行并提示剩余数量。
func TestRenderCapsSymbols(t *testing.T) {
	syms := make([]Symbol, MaxSymbols+5)
	for i := range syms {
		syms[i] = Symbol{
			Name:      fmt.Sprintf("F%03d", i),
			Kind:      "function",
			Signature: "func F(\n\ta int,\n\tb int,\n) error\n// doc comment",
			Line:      uint32(i + 1),
		}
	}
	out := Render(syms)
	if got := strings.Count(out, "L"); got != MaxSymbols {
		t.Errorf("最多渲染 %d 个符号, got %d", MaxSymbols, got)
	}
	if !strings.Contains(out, "... (5 more symbols)") {
		t.Errorf("应提示剩余 5 个符号, got:\n%s", out)
	}
	// 多行签名应压成一行，且不吞入括号平衡后的文档注释
	if strings.Contains(out, "doc comment") {
		t.Errorf("签名压缩不应包含括号平衡后的注释, got:\n%s", out)
	}
	if !strings.Contains(out, "func F( a int, b int, ) error") {
		t.Errorf("多行签名应合并为一行, got:\n%s", out)
	}
}
