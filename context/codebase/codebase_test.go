package codebase

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
)

func TestMain(m *testing.M) {
	openai.StartServerTask()
	m.Run()
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

// setupCodebase 创建测试用的配置和临时目录，返回目录路径和恢复函数
func setupCodebase(t *testing.T, dim int) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()

	restore := config.GlobalConfigSwap(structs.Config{
		Model: structs.ModelsConfig{
			EmbeddingModelID: 1,
			Models: map[int32]structs.ModelConfig{
				1: {
					ModelName: "test-embedding",
					ModelID:   "test-embedding",
					Type:      structs.ModelTypeEmbedding,
					ProviderURL:            "http://localhost:56108/v1",
					ProviderKey:            "sk-test",
					ProviderSpecificConfig: structs.ProviderSpecificConfig{Dimension: dim},
				},
			},
		},
	})

	if err := Initialize(); err != nil {
		restore()
		t.Fatalf("Initialize() failed: %v", err)
	}

	// 创建 CodebaseDB 实例，使其加入 VecDBs map
	if _, err := getOrCreateDB(tmpDir); err != nil {
		restore()
		t.Fatalf("getOrCreateDB failed: %v", err)
	}

	// 注册清理
	t.Cleanup(func() {
		closeDirectory(tmpDir)
		restore()
	})

	return tmpDir, restore
}

// ---------------------------------------------------------------------------
// Queue Tests
// ---------------------------------------------------------------------------

func TestQueuePushPop(t *testing.T) {
	q := NewQueueManager()

	if got := q.Len(); got != 0 {
		t.Fatalf("expected empty queue, got len=%d", got)
	}

	q.Push(&EmbedTask{EmbedText: "a", Symbol: "a", Priority: false})
	q.Push(&EmbedTask{EmbedText: "b", Symbol: "b", Priority: false})

	if got := q.Len(); got != 2 {
		t.Fatalf("expected len=2, got %d", got)
	}

	ctx := context.Background()
	task := q.WaitPop(ctx)
	if task == nil || task.Symbol != "a" {
		t.Fatalf("expected task 'a' first, got %v", task)
	}
	task = q.WaitPop(ctx)
	if task == nil || task.Symbol != "b" {
		t.Fatalf("expected task 'b' second, got %v", task)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("expected empty queue, got len=%d", got)
	}
}

func TestQueuePriority(t *testing.T) {
	q := NewQueueManager()

	// 普通入队：a, b
	q.Push(&EmbedTask{EmbedText: "a", Symbol: "a", Priority: false})
	q.Push(&EmbedTask{EmbedText: "b", Symbol: "b", Priority: false})
	// 插队：c 插到队头
	q.Push(&EmbedTask{EmbedText: "c", Symbol: "c", Priority: true})

	ctx := context.Background()
	// 期望顺序：c, a, b
	if task := q.WaitPop(ctx); task == nil || task.Symbol != "c" {
		t.Fatalf("expected 'c' (priority) first, got %v", task)
	}
	if task := q.WaitPop(ctx); task == nil || task.Symbol != "a" {
		t.Fatalf("expected 'a' second, got %v", task)
	}
	if task := q.WaitPop(ctx); task == nil || task.Symbol != "b" {
		t.Fatalf("expected 'b' third, got %v", task)
	}
}

func TestQueuePauseResume(t *testing.T) {
	q := NewQueueManager()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 暂停后入队
	q.Pause()
	q.Push(&EmbedTask{EmbedText: "a", Symbol: "a"})

	// WaitPop 应因暂停和超时而返回 nil
	task := q.WaitPop(ctx)
	if task != nil {
		t.Fatal("expected nil when paused and context times out")
	}

	// 恢复后应能取出
	ctx2 := context.Background()
	q.Resume()
	task = q.WaitPop(ctx2)
	if task == nil || task.Symbol != "a" {
		t.Fatalf("expected task 'a' after resume, got %v", task)
	}
}

func TestQueueClear(t *testing.T) {
	q := NewQueueManager()
	q.Push(&EmbedTask{EmbedText: "a", Symbol: "a"})
	q.Push(&EmbedTask{EmbedText: "b", Symbol: "b"})
	q.Clear()

	if got := q.Len(); got != 0 {
		t.Fatalf("expected empty after clear, got len=%d", got)
	}
}

func TestQueueContextCancel(t *testing.T) {
	q := NewQueueManager()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	task := q.WaitPop(ctx)
	if task != nil {
		t.Fatal("expected nil when context already cancelled")
	}
}

// ---------------------------------------------------------------------------
// Schema Tests
// ---------------------------------------------------------------------------

func TestInitialize(t *testing.T) {
	dir, restore := setupCodebase(t, 4)
	defer restore()

	cdb, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatalf("getOrCreateDB failed: %v", err)
	}
	if cdb == nil {
		t.Fatal("expected non-nil CodebaseDB")
	}
	if cdb.dimension != 4 {
		t.Fatalf("expected dimension=4, got %d", cdb.dimension)
	}
	if cdb.modelName != "test-embedding" {
		t.Fatalf("expected modelName=test-embedding, got %s", cdb.modelName)
	}
}

func TestSchemaCreate(t *testing.T) {
	dir, restore := setupCodebase(t, 8)
	defer restore()

	cdb, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatalf("getOrCreateDB failed: %v", err)
	}

	// 验证表已创建
	var tables []string
	rows, err := cdb.db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}

	hasMeta := false
	hasItems := false
	for _, name := range tables {
		if name == "codebase_meta" {
			hasMeta = true
		}
		if name == "codebase_items" {
			hasItems = true
		}
	}
	if !hasMeta {
		t.Fatal("codebase_meta table not found")
	}
	if !hasItems {
		t.Fatal("codebase_items table not found")
	}

	// 验证 meta
	modelName, _ := readMeta(cdb.db, "model_name")
	if modelName != "test-embedding" {
		t.Fatalf("expected model_name=test-embedding, got %s", modelName)
	}

	dimStr, _ := readMeta(cdb.db, "dimension")
	if dimStr != "8" {
		t.Fatalf("expected dimension=8, got %s", dimStr)
	}
}

func TestSchemaMigrationDimChange(t *testing.T) {
	dir, restore := setupCodebase(t, 4)
	defer restore()

	// 创建 dim=4 的库
	cdb, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatalf("getOrCreateDB failed: %v", err)
	}
	cdb.stopWorker()
	closeDirectory(dir)

	// 修改配置为 dim=8
	oldDim := openai.EmbeddingDim
	openai.EmbeddingDim = 8
	defer func() { openai.EmbeddingDim = oldDim }()

	config.GlobalConfigSwap(structs.Config{
		Model: structs.ModelsConfig{
			EmbeddingModelID: 1,
			Models: map[int32]structs.ModelConfig{
				1: {
					ModelName: "test-embedding",
					ModelID:   "test-embedding",
					Type:      structs.ModelTypeEmbedding,
					ProviderURL:            "http://localhost:56108/v1",
					ProviderKey:            "sk-test",
					ProviderSpecificConfig: structs.ProviderSpecificConfig{Dimension: 8},
				},
			},
		},
	})

	// 调用 Initialize 让 embedDim=8
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	// 重建（dim=8）
	cdb2, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatalf("getOrCreateDB after dim change failed: %v", err)
	}
	cdb2.stopWorker()

	// 验证 meta 已更新
	dimStr, _ := readMeta(cdb2.db, "dimension")
	if dimStr != "8" {
		t.Fatalf("expected dimension=8 after migration, got %s", dimStr)
	}
	if cdb2.dimension != 8 {
		t.Fatalf("expected cdb.dimension=8, got %d", cdb2.dimension)
	}
}

func TestSchemaMigrationModelNameChange(t *testing.T) {
	dir, restore := setupCodebase(t, 4)
	defer restore()

	cdb, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatalf("getOrCreateDB failed: %v", err)
	}
	cdb.stopWorker()
	closeDirectory(dir)

	// 修改 modelName
	config.GlobalConfigSwap(structs.Config{
		Model: structs.ModelsConfig{
			EmbeddingModelID: 1,
			Models: map[int32]structs.ModelConfig{
				1: {
					ModelName: "new-embedding-model",
					ModelID:   "test-embedding",
					Type:      structs.ModelTypeEmbedding,
					ProviderURL:            "http://localhost:56108/v1",
					ProviderKey:            "sk-test",
					ProviderSpecificConfig: structs.ProviderSpecificConfig{Dimension: 4},
				},
			},
		},
	})

	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	cdb2, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatalf("getOrCreateDB after model change failed: %v", err)
	}
	cdb2.stopWorker()

	storedName, _ := readMeta(cdb2.db, "model_name")
	if storedName != "new-embedding-model" {
		t.Fatalf("expected model_name=new-embedding-model, got %s", storedName)
	}
}

// ---------------------------------------------------------------------------
// Integration: Full Embed Pipeline
// ---------------------------------------------------------------------------

func TestEmbedPipeline(t *testing.T) {
	oldDim := openai.EmbeddingDim
	openai.EmbeddingDim = 4
	defer func() { openai.EmbeddingDim = oldDim }()

	dir, restore := setupCodebase(t, 4)
	defer restore()

	done := make(chan struct{})
	err := AddToQueue(dir, EmbedTask{
		EmbedText:   "test function that does something",
		FullContent: "func Test() { return 1 }",
		FilePath:    "test.go",
		Symbol:      "Test",
		Tags:        []string{"go", "function"},
		Done:        done,
	})
	if err != nil {
		t.Fatalf("AddToQueue failed: %v", err)
	}

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for embed task completion")
	}

	// 验证 DB 中有记录
	cdb := VecDBs[dir]
	if cdb == nil {
		t.Fatal("CodebaseDB not found in VecDBs")
	}

	hash, err := cdb.checkExistingHash("test.go", "Test")
	if err != nil {
		t.Fatalf("checkExistingHash failed: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty embed_hash, got empty")
	}

	// 验证 full_content 正确存储
	var fullContent string
	err = cdb.db.QueryRow(
		"SELECT full_content FROM codebase_items WHERE file_path=? AND symbol=?",
		"test.go", "Test",
	).Scan(&fullContent)
	if err != nil {
		t.Fatalf("query full_content failed: %v", err)
	}
	if fullContent != "func Test() { return 1 }" {
		t.Fatalf("expected full_content='func Test() { return 1 }', got '%s'", fullContent)
	}

	// 验证 vec0 表有对应的向量
	var vecID int64
	err = cdb.db.QueryRow(
		"SELECT id FROM codebase_vec WHERE id=(SELECT id FROM codebase_items WHERE file_path=? AND symbol=?)",
		"test.go", "Test",
	).Scan(&vecID)
	if err == sql.ErrNoRows {
		t.Fatal("vector not found in codebase_vec")
	}
	if err != nil {
		t.Fatalf("query vec failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Hash Dedup
// ---------------------------------------------------------------------------

func TestHashDedup(t *testing.T) {
	oldDim := openai.EmbeddingDim
	openai.EmbeddingDim = 4
	defer func() { openai.EmbeddingDim = oldDim }()

	dir, restore := setupCodebase(t, 4)
	defer restore()

	// 第一次嵌入
	done1 := make(chan struct{})
	if err := AddToQueue(dir, EmbedTask{
		EmbedText:   "same content",
		FullContent: "full A",
		FilePath:    "file.go",
		Symbol:      "FuncA",
		Done:        done1,
	}); err != nil {
		t.Fatal(err)
	}
	<-done1

	// 获取第一次的 hash
	cdb := VecDBs[dir]
	hash1, _ := cdb.checkExistingHash("file.go", "FuncA")

	// 第二次嵌入相同内容
	done2 := make(chan struct{})
	if err := AddToQueue(dir, EmbedTask{
		EmbedText:   "same content", // 相同
		FullContent: "full B",       // 全量内容更新
		FilePath:    "file.go",
		Symbol:      "FuncA",
		Done:        done2,
	}); err != nil {
		t.Fatal(err)
	}
	<-done2

	// hash 应相同
	hash2, _ := cdb.checkExistingHash("file.go", "FuncA")
	if hash1 != hash2 {
		t.Fatalf("expected same hash after dedup, got %s vs %s", hash1, hash2)
	}

	// full_content 应已更新为 "full B"
	var fc string
	cdb.db.QueryRow(
		"SELECT full_content FROM codebase_items WHERE file_path=? AND symbol=?",
		"file.go", "FuncA",
	).Scan(&fc)
	if fc != "full B" {
		t.Fatalf("expected full_content='full B' after update, got '%s'", fc)
	}
}

// ---------------------------------------------------------------------------
// CleanSymbols
// ---------------------------------------------------------------------------

func TestCleanSymbols(t *testing.T) {
	oldDim := openai.EmbeddingDim
	openai.EmbeddingDim = 4
	defer func() { openai.EmbeddingDim = oldDim }()

	dir, restore := setupCodebase(t, 4)
	defer restore()

	// 直接操作 DB 插入多条 symbol
	cdb, err := getOrCreateDB(dir)
	if err != nil {
		t.Fatal(err)
	}

	// 插入测试数据
	inserts := []struct {
		symbol string
		tags   string
	}{
		{"", "[]"},          // 整个文件
		{"FuncA", "[]"},     // 保留
		{"FuncB", "[]"},     // 保留
		{"FuncC", "[]"},     // 删除
		{"FuncD", "[]"},     // 删除
	}

	for _, ins := range inserts {
		h := embedHash("content-" + ins.symbol)
		_, err := cdb.db.Exec(
			`INSERT INTO codebase_items (file_path, symbol, tags, full_content, embed_text, embed_hash)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			"main.go", ins.symbol, ins.tags, "content-"+ins.symbol,
			"content-"+ins.symbol, h,
		)
		if err != nil {
			t.Fatalf("insert %s: %v", ins.symbol, err)
		}
	}

	// 清理：只保留 FuncA 和 FuncB
	if err := CleanSymbols(dir, "main.go", []string{"FuncA", "FuncB"}); err != nil {
		t.Fatalf("CleanSymbols failed: %v", err)
	}

	// 验证
	rows, err := cdb.db.Query(
		"SELECT symbol FROM codebase_items WHERE file_path=? ORDER BY symbol",
		"main.go",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var remaining []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		remaining = append(remaining, s)
	}

	// 应只剩：""（整个文件）、FuncA、FuncB
	if len(remaining) != 3 {
		t.Fatalf("expected 3 remaining, got %d: %v", len(remaining), remaining)
	}
	if remaining[0] != "" {
		t.Fatalf("expected first to be empty symbol (whole file), got '%s'", remaining[0])
	}
}

// ---------------------------------------------------------------------------
// Directory Lifecycle
// ---------------------------------------------------------------------------

func TestDirectoryStopResume(t *testing.T) {
	dir, restore := setupCodebase(t, 4)
	defer restore()

	// 检查初始状态
	status := DirectoryStatus(dir)
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if !status.WorkerActive {
		t.Fatal("expected worker active after init")
	}

	// 停止
	if err := StopDirectory(dir); err != nil {
		t.Fatalf("StopDirectory failed: %v", err)
	}
	status = DirectoryStatus(dir)
	if status.WorkerActive {
		t.Fatal("expected worker stopped")
	}

	// 恢复
	if err := ResumeDirectory(dir); err != nil {
		t.Fatalf("ResumeDirectory failed: %v", err)
	}
	status = DirectoryStatus(dir)
	if !status.WorkerActive {
		t.Fatal("expected worker active after resume")
	}
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

func TestGetDimFromCfg(t *testing.T) {
	// 设置一个有效的 embedding 配置
	restore := config.GlobalConfigSwap(structs.Config{
		Model: structs.ModelsConfig{
			EmbeddingModelID: 1,
			Models: map[int32]structs.ModelConfig{
				1: {
					ModelName: "test-embedding",
					ModelID:   "test-embedding",
					Type:      structs.ModelTypeEmbedding,
					ProviderSpecificConfig: structs.ProviderSpecificConfig{Dimension: 128},
				},
			},
		},
	})
	defer restore()

	dim := GetDimFromCfg()
	if dim != 128 {
		t.Fatalf("expected 128, got %d", dim)
	}
}

// ---------------------------------------------------------------------------
// Embed Task Tags
// ---------------------------------------------------------------------------

func TestTagsToJSON(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{nil, "[]"},
		{[]string{}, "[]"},
		{[]string{"go"}, `["go"]`},
		{[]string{"go", "test"}, `["go","test"]`},
		{[]string{"a\"b"}, `["a\"b"]`},
	}
	for _, tt := range tests {
		got := tagsToJSON(tt.input)
		if got != tt.want {
			t.Errorf("tagsToJSON(%v) = %s, want %s", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// DB 文件位置
// ---------------------------------------------------------------------------

func TestDBPath(t *testing.T) {
	tmpDir := t.TempDir()
	expected := filepath.Join(tmpDir, ".alkaid0", "codebase.sqlite")

	restore := config.GlobalConfigSwap(structs.Config{
		Model: structs.ModelsConfig{
			EmbeddingModelID: 1,
			Models: map[int32]structs.ModelConfig{
				1: {
					ModelName: "test-embedding",
					ModelID:   "test-embedding",
					Type:      structs.ModelTypeEmbedding,
					ProviderURL:            "http://localhost:56108/v1",
					ProviderKey:            "sk-test",
					ProviderSpecificConfig: structs.ProviderSpecificConfig{Dimension: 4},
				},
			},
		},
	})
	defer restore()

	Initialize()
	cdb, err := getOrCreateDB(tmpDir)
	if err != nil {
		t.Fatalf("getOrCreateDB failed: %v", err)
	}

	// 关闭后检查文件是否存在
	cdb.stopWorker()
	closeDirectory(tmpDir)

	if _, err := os.Stat(expected); os.IsNotExist(err) {
		t.Fatalf("db file not created at %s", expected)
	}
}
