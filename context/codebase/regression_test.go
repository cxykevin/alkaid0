package codebase

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
)

// ---------------------------------------------------------------------------
// 深度审查 P1-26 / P2 修复的回归测试
// ---------------------------------------------------------------------------

// setupBM25Only 配置无 embedding 模型的 BM25-only 环境，返回临时目录。
// 该模式下 worker 同步写入 codebase_items，便于验证扫描/清理逻辑。
func setupBM25Only(t *testing.T) string {
	t.Helper()
	restore := config.GlobalConfigSwap(structs.Config{
		Model: structs.ModelsConfig{
			Models: map[int32]structs.ModelConfig{
				1: {ModelName: "test-llm", ModelID: "test-llm", Type: ""},
			},
		},
	})
	embedModelCfg = nil
	embedDim = 0

	tmpDir := t.TempDir()
	if _, err := getOrCreateDB(tmpDir); err != nil {
		restore()
		t.Fatalf("getOrCreateDB failed: %v", err)
	}
	t.Cleanup(func() {
		closeDirectory(tmpDir)
		restore()
	})
	return tmpDir
}

// waitForItem 等待指定路径的条目写入数据库（worker 异步消费队列），超时即失败。
func waitForItem(t *testing.T, dir, filePath string, timeout time.Duration) {
	t.Helper()
	if !itemExists(t, dir, filePath, timeout) {
		t.Fatalf("timeout waiting for item %s", filePath)
	}
}

// itemExists 轮询查询指定路径的条目是否存在
func itemExists(t *testing.T, dir, filePath string, timeout time.Duration) bool {
	t.Helper()
	cdb := VecDBs[dir]
	if cdb == nil {
		t.Fatalf("codebase db for %s not found", dir)
	}
	deadline := time.Now().Add(timeout)
	for {
		var cnt int
		if err := cdb.db.QueryRow(
			"SELECT COUNT(*) FROM codebase_items WHERE file_path=?", filePath,
		).Scan(&cnt); err == nil && cnt > 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitTaskDone 等待任务完成信号
func waitTaskDone(t *testing.T, done <-chan struct{}, timeout time.Duration, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for %s", what)
	}
}

// TestEmbeddingDimensionMismatchRejected P1-26：嵌入 API 返回的维度与索引配置不一致时，
// 必须拒绝写入向量、保留条目（降级 BM25）并把 embed_hash 留空以便后续重试，
// 而不是把错误维度的数据写入 vec0 或直接丢弃条目。
func TestEmbeddingDimensionMismatchRejected(t *testing.T) {
	oldDim := openai.EmbeddingDim
	openai.EmbeddingDim = 8 // 模型实际返回 8 维，索引配置为 4 维
	defer func() { openai.EmbeddingDim = oldDim }()

	dir, restore := setupCodebase(t, 4)
	defer restore()

	done := make(chan struct{})
	if err := AddToQueue(dir, EmbedTask{
		EmbedText:   "func Dim() {}",
		FullContent: "func Dim() {}",
		FilePath:    "dim.go",
		Symbol:      "Dim",
		Done:        done,
	}); err != nil {
		t.Fatalf("AddToQueue failed: %v", err)
	}
	waitTaskDone(t, done, 30*time.Second, "dimension mismatch task")

	cdb := VecDBs[dir]

	// 1) 维度不匹配的向量不得进入 vec0（索引污染）
	var vecCnt int
	if err := cdb.db.QueryRow(
		"SELECT COUNT(*) FROM codebase_vec",
	).Scan(&vecCnt); err != nil {
		t.Fatalf("query vec count: %v", err)
	}
	if vecCnt != 0 {
		t.Fatalf("维度不匹配的向量被写入 vec0: count=%d", vecCnt)
	}

	// 2) 条目不能被丢弃（应保留内容供 BM25 检索）
	var hash string
	err := cdb.db.QueryRow(
		"SELECT embed_hash FROM codebase_items WHERE file_path=? AND symbol=?",
		"dim.go", "Dim",
	).Scan(&hash)
	if err == sql.ErrNoRows {
		t.Fatal("嵌入失败的条目被丢弃：codebase_items 中没有该条目")
	}
	if err != nil {
		t.Fatalf("query item: %v", err)
	}
	// 3) 不得标记为已嵌入完成（hash 非空会让后续索引跳过重试）
	if hash != "" {
		t.Fatalf("维度不匹配的条目仍被标记为已完成嵌入（embed_hash=%q）", hash)
	}
}

// TestVectorSearchDimensionMismatchDefense P1-26：查询嵌入维度与索引不一致时，
// 向量检索必须给出明确的维度错误，而不是返回随机/错误结果。
func TestVectorSearchDimensionMismatchDefense(t *testing.T) {
	oldDim := openai.EmbeddingDim
	openai.EmbeddingDim = 4
	defer func() { openai.EmbeddingDim = oldDim }()

	dir, restore := setupCodebase(t, 4)
	defer restore()

	done := make(chan struct{})
	if err := AddToQueue(dir, EmbedTask{
		EmbedText:   "func Vec() {}",
		FullContent: "func Vec() {}",
		FilePath:    "vec.go",
		Symbol:      "Vec",
		Done:        done,
	}); err != nil {
		t.Fatalf("AddToQueue failed: %v", err)
	}
	waitTaskDone(t, done, 30*time.Second, "vector seed task")

	// 之后查询模型返回 8 维，与索引的 4 维不一致
	openai.EmbeddingDim = 8

	cdb := VecDBs[dir]
	_, err := cdb.VectorSearch(context.Background(), "func Vec", 5)
	if err == nil {
		t.Fatal("查询向量维度不匹配时 VectorSearch 应返回错误")
	}
	if !strings.Contains(err.Error(), "index expects") {
		t.Fatalf("错误信息应明确指出维度不匹配（含 index expects），got: %v", err)
	}
}

// TestRunIndexReportsEmbeddingFailure P2：嵌入失败的条目不能被丢弃、索引状态
// 不能仍报 completed（要与真实结果一致）。
func TestRunIndexReportsEmbeddingFailure(t *testing.T) {
	oldDim := openai.EmbeddingDim
	openai.EmbeddingDim = 8
	defer func() { openai.EmbeddingDim = oldDim }()

	dir, restore := setupCodebase(t, 4)
	defer restore()

	if err := os.WriteFile(filepath.Join(dir, "fail.txt"), []byte("content that cannot embed"), 0o644); err != nil {
		t.Fatal(err)
	}

	terminal := make(chan IndexStatus, 4)
	broadcastFn := func(s IndexStatus) {
		if s.Status == "completed" || s.Status == "error" {
			select {
			case terminal <- s:
			default:
			}
		}
	}

	if err := RunIndex(context.Background(), dir, broadcastFn); err != nil {
		t.Fatalf("RunIndex failed: %v", err)
	}

	select {
	case s := <-terminal:
		if s.Status != "error" {
			t.Fatalf("嵌入全部失败时索引状态不应是 %q（应与真实结果一致地报告 error），status=%+v", s.Status, s)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("timeout waiting for terminal index status")
	}

	// 条目内容不应被丢弃
	if !itemExists(t, dir, "fail.txt", 10*time.Second) {
		t.Fatal("嵌入失败的条目被丢弃：codebase_items 中没有该文件")
	}
}

// TestAddToQueueAfterWorkerCancelled P2：stopWorker 已发出 cancel、但状态尚未清理
// （停止与清理之间的窗口）时入队的任务必须有消费者，不能被丢唤醒导致队列永久卡住。
func TestAddToQueueAfterWorkerCancelled(t *testing.T) {
	dir := setupBM25Only(t)
	cdb := VecDBs[dir]

	cdb.mu.Lock()
	cancel := cdb.workerCancel
	cdb.mu.Unlock()
	if cancel == nil {
		t.Fatal("worker should be running after getOrCreateDB")
	}

	// 模拟 stopWorker 的第一步：cancel 已发出，但 workerCancel 仍非 nil。
	// worker 会在收到 cancel 后退出，此时状态尚未被 stopWorker 清理。
	cancel()
	time.Sleep(300 * time.Millisecond)

	done := make(chan struct{})
	if err := AddToQueue(dir, EmbedTask{
		EmbedText:   "after cancel",
		FullContent: "after cancel",
		FilePath:    "after_cancel.txt",
		Done:        done,
	}); err != nil {
		t.Fatalf("AddToQueue failed: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("取消进行中入队的任务永久卡住：worker 未被重启消费队列")
	}
}

// TestRunIndexDoesNotClobberActiveCancel P2：第二次 RunIndex 被并发锁拒绝时，
// 不得覆盖第一次运行的活动 cancel 句柄，否则 /index cancel 无法取消正在进行的索引。
func TestRunIndexDoesNotClobberActiveCancel(t *testing.T) {
	dir := setupBM25Only(t)
	if err := os.WriteFile(filepath.Join(dir, "cancel.txt"), []byte("cancel me"), 0o644); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	blocking := func(IndexStatus) {
		once.Do(func() {
			close(entered)
			<-release
		})
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunIndex(context.Background(), dir, blocking)
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for first RunIndex to start scanning")
	}

	// 第二次 RunIndex 应被并发锁拒绝，且不能动第一次的 cancel 句柄
	err := RunIndex(context.Background(), dir, nil)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second RunIndex should fail with 'already running', got: %v", err)
	}

	if err := CancelIndex(dir); err != nil {
		t.Fatalf("CancelIndex failed: %v", err)
	}
	close(release)

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("第一次 RunIndex 未被取消（cancel 句柄被第二次 RunIndex 覆盖）：err=%v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for first RunIndex to return")
	}
}

// TestRunIndexSkipsSymlink P2：工作区内指向外部的符号链接不得被跟随读取/索引。
func TestRunIndexSkipsSymlink(t *testing.T) {
	dir := setupBM25Only(t)

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("outside-workspace-secret-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(dir, "evil.txt")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("inside workspace content"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunIndex(context.Background(), dir, nil); err != nil {
		t.Fatalf("RunIndex failed: %v", err)
	}

	// real.txt 在遍历顺序上晚于 evil.txt，它入库即可确认前序任务已处理完
	waitForItem(t, dir, "real.txt", 10*time.Second)

	if itemExists(t, dir, "evil.txt", 0) {
		t.Fatal("RunIndex 跟随了符号链接，读取并索引了工作区外的文件")
	}
}

// TestRunIndexNestedGitignoreAnchored P2：嵌套 .gitignore 中的 /name 规则
// 应相对该 .gitignore 所在目录锚定，而不是被当成索引根目录的锚定规则。
func TestRunIndexNestedGitignoreAnchored(t *testing.T) {
	dir := setupBM25Only(t)

	if err := os.MkdirAll(filepath.Join(dir, "secretdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub", "secretdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secretdir", "y.txt"), []byte("root level y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "secretdir", "x.txt"), []byte("nested ignored x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", ".gitignore"), []byte("/secretdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// zzz.txt 在遍历顺序上最后，它入库说明其余候选文件都已处理
	if err := os.WriteFile(filepath.Join(dir, "zzz.txt"), []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunIndex(context.Background(), dir, nil); err != nil {
		t.Fatalf("RunIndex failed: %v", err)
	}
	waitForItem(t, dir, "zzz.txt", 10*time.Second)

	if itemExists(t, dir, "sub/secretdir/x.txt", 0) {
		t.Fatal("嵌套 .gitignore 的 /secretdir 规则未生效：sub/secretdir/x.txt 被索引")
	}
	if !itemExists(t, dir, "secretdir/y.txt", 0) {
		t.Fatal("嵌套 .gitignore 的 /secretdir 被当成根锚定规则：根目录下的 secretdir/y.txt 被误忽略")
	}
}

// TestRunIndexKeepsSyntheticRows P2：tempfs/chathistory 等由索引流程合成的
// 非磁盘条目不能被每轮 RunIndex 的"已删除文件"清理误删（无法从磁盘重建）。
func TestRunIndexKeepsSyntheticRows(t *testing.T) {
	dir := setupBM25Only(t)

	synthetic := []string{"tempfs/1/a.txt", "chathistory/1"}
	for _, p := range synthetic {
		done := make(chan struct{})
		if err := AddToQueue(dir, EmbedTask{
			EmbedText:   "synthetic " + p,
			FullContent: "synthetic " + p,
			FilePath:    p,
			Tags:        []string{"tempfs"},
			Done:        done,
		}); err != nil {
			t.Fatalf("AddToQueue(%s) failed: %v", p, err)
		}
		waitTaskDone(t, done, 10*time.Second, "synthetic task "+p)
	}

	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("real file"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunIndex(context.Background(), dir, nil); err != nil {
		t.Fatalf("RunIndex failed: %v", err)
	}
	waitForItem(t, dir, "real.txt", 10*time.Second)

	for _, p := range synthetic {
		if !itemExists(t, dir, p, 0) {
			t.Fatalf("合成条目 %s 被 RunIndex 清扫（无法从磁盘重建）", p)
		}
	}
}
