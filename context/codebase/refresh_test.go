package codebase

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// ftsMatchCount 返回 FTS5 全文索引中匹配 term 的行数（BM25 是否已更新）。
func ftsMatchCount(t *testing.T, db *sql.DB, term string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM codebase_fts WHERE codebase_fts MATCH ?`, term).Scan(&n); err != nil {
		t.Fatalf("fts match %q: %v", term, err)
	}
	return n
}

// TestRefreshFileUpdatesBM25 刷新后 BM25 立即可查新内容；旧符号被清理。
func TestRefreshFileUpdatesBM25(t *testing.T) {
	dir, _ := setupCodebase(t, 8)
	rel := "pkg/a.go"
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "package pkg\n\nfunc Old() {}\n"
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := RefreshFile(dir, rel, content, []FileSymbol{
		{Name: "Old", Kind: "function", Signature: "func Old()", Code: "func Old() {}"},
	}); err != nil {
		t.Fatalf("RefreshFile: %v", err)
	}

	cdb, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatalf("getOrCreateDB: %v", err)
	}
	if got := ftsMatchCount(t, cdb.db, "Old"); got == 0 {
		t.Fatal("刷新后 BM25 应立即可命中旧符号")
	}
	var n int
	if err := cdb.db.QueryRow(
		`SELECT COUNT(*) FROM codebase_items WHERE file_path=? AND symbol='Old'`, rel,
	).Scan(&n); err != nil {
		t.Fatalf("query items: %v", err)
	}
	if n != 1 {
		t.Fatalf("符号条目应为 1 条, got %d", n)
	}

	// 改写文件：旧符号消失、新符号出现
	content = "package pkg\n\nfunc New() {}\n"
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := RefreshFile(dir, rel, content, []FileSymbol{
		{Name: "New", Kind: "function", Signature: "func New()", Code: "func New() {}"},
	}); err != nil {
		t.Fatalf("second RefreshFile: %v", err)
	}
	if got := ftsMatchCount(t, cdb.db, "Old"); got != 0 {
		t.Errorf("旧符号应从 BM25 删除, got %d", got)
	}
	if got := ftsMatchCount(t, cdb.db, "New"); got == 0 {
		t.Error("新符号应可被 BM25 命中")
	}
}

// TestFileLockBlocksIndexAndBumpsSeq 刷新持写锁时索引跳过；锁释放后代次递增。
func TestFileLockBlocksIndexAndBumpsSeq(t *testing.T) {
	key := fileKey(t.TempDir(), "a.go")
	before := fileRefreshSeq(key)

	unlock, seq := lockFileForRefresh(key)
	if seq != before+1 {
		t.Fatalf("加写锁时应递增代次, got %d want %d", seq, before+1)
	}
	if _, ok := tryLockFileForIndex(key); ok {
		t.Fatal("刷新持写锁时索引不应拿到读锁（应跳过该文件）")
	}

	unlock()
	unlockIdx, ok := tryLockFileForIndex(key)
	if !ok {
		t.Fatal("锁释放后索引应能拿到读锁")
	}
	unlockIdx()
	if fileRefreshSeq(key) != seq {
		t.Fatalf("代次不应在解锁时再次变化, got %d want %d", fileRefreshSeq(key), seq)
	}
}

// TestStaleEmbedTaskDropped 旧代次的向量任务在 worker 侧被丢弃，不写回已删除的符号。
func TestStaleEmbedTaskDropped(t *testing.T) {
	dir, _ := setupCodebase(t, 8)
	rel := "b.go"
	content := "package b\n\nfunc Keep() {}\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cdb, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatalf("getOrCreateDB: %v", err)
	}

	// 先刷新一次（代次 N），再刷新一次（代次 N+1）：第一条任务已过期。
	if err := RefreshFile(dir, rel, content, []FileSymbol{
		{Name: "Keep", Kind: "function", Signature: "func Keep()", Code: "func Keep() {}"},
	}); err != nil {
		t.Fatalf("RefreshFile: %v", err)
	}
	staleSeq := fileRefreshSeq(fileKey(dir, rel))
	if err := RefreshFile(dir, rel, content, []FileSymbol{
		{Name: "Keep", Kind: "function", Signature: "func Keep()", Code: "func Keep() {}"},
	}); err != nil {
		t.Fatalf("second RefreshFile: %v", err)
	}

	// 构造一个过期任务：内容含已删除符号，代次落后
	stale := &EmbedTask{
		EmbedText:   "func Gone()",
		FullContent: "func Gone() {}",
		FilePath:    rel,
		Symbol:      "Gone",
		Tags:        []string{"function"},
		Seq:         staleSeq,
	}
	cdb.processTask(context.Background(), stale)

	var n int
	if err := cdb.db.QueryRow(
		`SELECT COUNT(*) FROM codebase_items WHERE file_path=? AND symbol='Gone'`, rel,
	).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatal("过期任务不应把已删除符号写回索引")
	}
	if got := ftsMatchCount(t, cdb.db, "Gone"); got != 0 {
		t.Errorf("过期任务不应污染 BM25, got %d", got)
	}
}
