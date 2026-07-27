package codebase

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
)

// CodebaseDB 单个目录的向量数据库实例
type CodebaseDB struct {
	directory string
	db        *sql.DB

	// 嵌入模型信息（初始化时从配置快照，用于 schema 校验和 API 调用）
	modelName   string
	modelID     string
	dimension   int
	providerURL string
	providerKey string

	queue *QueueManager

	workerCtx    context.Context
	workerCancel context.CancelFunc
	workerWG     sync.WaitGroup

	mu sync.RWMutex

	logger *log.LogsObj
}

// DirStatus 目录嵌入处理状态
type DirStatus struct {
	Directory    string `json:"directory"`
	QueueLen     int    `json:"queue_len"`
	WorkerActive bool   `json:"worker_active"`
	Paused       bool   `json:"paused"`
}

// Initialize 从全局配置读取嵌入模型信息，初始化 codebase 包
// 必须在调用其他任何函数前调用
func Initialize() error {
	mc, err := resolveModelFromCfg()
	if err != nil {
		return fmt.Errorf("codebase init: %w", err)
	}

	embedModelCfg = mc
	embedDim = mc.ProviderSpecificConfig.Dimension
	if embedDim <= 0 {
		embedDim = DefaultDim
	}

	logger.Info("codebase initialized: model=%s dim=%d", mc.ModelName, embedDim)
	return nil
}

// Shutdown 关闭所有目录的数据库连接和 worker
func Shutdown() error {
	VecDBsLock.Lock()
	dirs := make([]string, 0, len(VecDBs))
	for d := range VecDBs {
		dirs = append(dirs, d)
	}
	VecDBsLock.Unlock()

	var lastErr error
	for _, dir := range dirs {
		if err := closeDirectory(dir); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// AddToQueue 将嵌入任务加入指定目录的队列
// task.Priority=true 时插队到队头，否则加入队尾
func AddToQueue(directory string, task EmbedTask) error {
	cdb, err := getOrCreateDB(directory)
	if err != nil {
		return err
	}

	cdb.mu.RLock()
	q := cdb.queue
	cdb.mu.RUnlock()

	if q == nil {
		return fmt.Errorf("queue not ready for directory %s", directory)
	}

	q.Push(&task)
	return nil
}

// StopDirectory 停止指定目录的 worker，清空队列中待处理的任务
func StopDirectory(directory string) error {
	cdb, err := getOrCreateDB(directory)
	if err != nil {
		return err
	}
	cdb.stopWorker()

	cdb.mu.Lock()
	if cdb.queue != nil {
		cdb.queue.Clear()
	}
	cdb.mu.Unlock()
	return nil
}

// ResumeDirectory 恢复指定目录的队列处理（重新启动 worker）
func ResumeDirectory(directory string) error {
	cdb, err := getOrCreateDB(directory)
	if err != nil {
		return err
	}
	cdb.startWorker()
	return nil
}

// DirectoryStatus 获取指定目录的处理状态
func DirectoryStatus(directory string) *DirStatus {
	VecDBsLock.Lock()
	cdb, ok := VecDBs[directory]
	VecDBsLock.Unlock()

	if !ok {
		return &DirStatus{
			Directory: directory,
		}
	}

	cdb.mu.RLock()
	defer cdb.mu.RUnlock()

	status := &DirStatus{
		Directory:    directory,
		WorkerActive: cdb.workerCancel != nil,
		Paused:       cdb.workerCancel == nil,
	}
	if cdb.queue != nil {
		status.QueueLen = cdb.queue.Len()
	}
	return status
}

// CleanSymbols 删除指定文件中不在 activeSymbols 列表中的所有符号片段
// symbol=""（整个文件）的记录不会被删除；同时清理对应的 vec0 向量
func CleanSymbols(directory, filePath string, activeSymbols []string) error {
	cdb, err := getOrCreateDB(directory)
	if err != nil {
		return err
	}

	cdb.mu.Lock()
	defer cdb.mu.Unlock()

	if err := cdb.ensureDBOpen(); err != nil {
		return err
	}

	tx, err := cdb.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var idsToDelete []int64
	if len(activeSymbols) == 0 {
		// 无活跃符号，删除所有非空符号的记录
		rows, err := tx.Query(
			"SELECT id FROM codebase_items WHERE file_path=? AND symbol!=''",
			filePath,
		)
		if err != nil {
			return fmt.Errorf("query orphan ids: %w", err)
		}
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			idsToDelete = append(idsToDelete, id)
		}
		rows.Close()

		if _, err := tx.Exec(
			"DELETE FROM codebase_items WHERE file_path=? AND symbol!=''",
			filePath,
		); err != nil {
			return fmt.Errorf("delete orphan items: %w", err)
		}
	} else {
		// 构建 IN 占位符
		placeholders := make([]string, len(activeSymbols))
		args := make([]any, 0, len(activeSymbols)+1)
		args = append(args, filePath)
		for i, s := range activeSymbols {
			placeholders[i] = "?"
			args = append(args, s)
		}

		inClause := joinPlaceholders(placeholders)

		// 先查需要删除的 ID
		query := fmt.Sprintf(
			"SELECT id FROM codebase_items WHERE file_path=? AND symbol!='' AND symbol NOT IN (%s)",
			inClause,
		)
		rows, err := tx.Query(query, args...)
		if err != nil {
			return fmt.Errorf("query orphan ids: %w", err)
		}
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			idsToDelete = append(idsToDelete, id)
		}
		rows.Close()

		// 删除 orphan items
		delQuery := fmt.Sprintf(
			"DELETE FROM codebase_items WHERE file_path=? AND symbol!='' AND symbol NOT IN (%s)",
			inClause,
		)
		if _, err := tx.Exec(delQuery, args...); err != nil {
			return fmt.Errorf("delete orphan items: %w", err)
		}
	}

	// 清理 vec0 中的对应向量
	for _, id := range idsToDelete {
		if _, err := tx.Exec("DELETE FROM codebase_vec WHERE id=?", id); err != nil {
			// vec0 delete 可能因记录不存在而影响 0 行，忽略错误
			cdb.logger.Warn("clean vec0 id=%d: %v", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if n := len(idsToDelete); n > 0 {
		cdb.logger.Info("clean %s: removed %d orphaned symbols", filePath, n)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 内部函数
// ---------------------------------------------------------------------------

// getOrCreateDB 获取或创建指定目录的 CodebaseDB 实例
func getOrCreateDB(directory string) (*CodebaseDB, error) {
	VecDBsLock.Lock()
	if cdb, ok := VecDBs[directory]; ok {
		VecDBsLock.Unlock()
		return cdb, nil
	}
	VecDBsLock.Unlock()

	if embedModelCfg == nil {
		return nil, fmt.Errorf("codebase not initialized, call Initialize() first")
	}

	cdb := &CodebaseDB{
		directory:   directory,
		modelName:   embedModelCfg.ModelName,
		modelID:     embedModelCfg.ModelID,
		dimension:   embedDim,
		providerURL: embedModelCfg.ProviderURL,
		providerKey: embedModelCfg.ProviderKey,
		queue:       NewQueueManager(),
		logger:      log.New(fmt.Sprintf("codebase:%s", filepath.Base(directory))),
	}

	// 如果单模型没有设置 URL/Key，使用全局默认
	cfg := config.GlobalConfigSafe()
	if cdb.providerURL == "" {
		cdb.providerURL = cfg.Model.ProviderURL
	}
	if cdb.providerKey == "" {
		cdb.providerKey = cfg.Model.ProviderKey
	}

	if err := cdb.openDB(); err != nil {
		return nil, fmt.Errorf("open db for %s: %w", directory, err)
	}
	if err := cdb.ensureSchema(); err != nil {
		cdb.db.Close()
		return nil, fmt.Errorf("schema for %s: %w", directory, err)
	}
	cdb.startWorker()

	VecDBsLock.Lock()
	VecDBs[directory] = cdb
	VecDBsLock.Unlock()

	cdb.logger.Info("codebase db created: dim=%d model=%s", cdb.dimension, cdb.modelName)
	return cdb, nil
}

// closeDirectory 关闭指定目录的 CodebaseDB
func closeDirectory(directory string) error {
	VecDBsLock.Lock()
	cdb, ok := VecDBs[directory]
	if !ok {
		VecDBsLock.Unlock()
		return nil
	}
	delete(VecDBs, directory)
	VecDBsLock.Unlock()

	cdb.stopWorker()

	cdb.mu.Lock()
	defer cdb.mu.Unlock()

	if cdb.db != nil {
		return cdb.db.Close()
	}
	return nil
}

// openDB 打开目录对应的 .alkaid0/codebase.sqlite
func (cdb *CodebaseDB) openDB() error {
	dbPath := filepath.Join(cdb.directory, ".alkaid0", "codebase.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("sql open: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// 验证连接
	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("db ping: %w", err)
	}

	// 开启 WAL 模式
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return fmt.Errorf("pragma wal: %w", err)
	}

	cdb.db = db
	return nil
}

// ensureDBOpen 确保数据库连接可用，在持有 mu 锁时调用
func (cdb *CodebaseDB) ensureDBOpen() error {
	if cdb.db != nil {
		return nil
	}
	return cdb.openDB()
}

// ensureSchema 创建或迁移数据库 schema
func (cdb *CodebaseDB) ensureSchema() error {
	// 检查 meta 表是否存在
	var tableName string
	err := cdb.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='codebase_meta'",
	).Scan(&tableName)

	if err == sql.ErrNoRows {
		// 新数据库，创建所有表
		return cdb.createTables()
	}
	if err != nil {
		return fmt.Errorf("check meta table: %w", err)
	}

	// 读取已存储的 model_name 和 dimension
	storedModelName, _ := readMeta(cdb.db, "model_name")
	storedDimStr, _ := readMeta(cdb.db, "dimension")

	if storedModelName == "" && storedDimStr == "" {
		// meta 表存在但无数据（非正常情况），重建
		return cdb.createTables()
	}

	storedDim, _ := strconv.Atoi(storedDimStr)

	if storedModelName != cdb.modelName || storedDim != cdb.dimension {
		cdb.logger.Info("schema changed: model %s/%d -> %s/%d, rebuilding",
			storedModelName, storedDim, cdb.modelName, cdb.dimension)
		if err := cdb.dropTables(); err != nil {
			return err
		}
		return cdb.createTables()
	}

	return nil
}

// createTables 创建所有表
func (cdb *CodebaseDB) createTables() error {
	// vec0 虚拟表（维度在 DDL 中固定）
	vecSQL := fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS codebase_vec USING vec0(
			id INTEGER PRIMARY KEY,
			embedding float[%d]
		)`, cdb.dimension)
	if _, err := cdb.db.Exec(vecSQL); err != nil {
		return fmt.Errorf("create vec table: %w", err)
	}

	// items 表
	if _, err := cdb.db.Exec(`
		CREATE TABLE IF NOT EXISTS codebase_items(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL,
			symbol TEXT NOT NULL DEFAULT '',
			tags TEXT DEFAULT '',
			full_content TEXT NOT NULL,
			embed_text TEXT NOT NULL,
			embed_hash TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(file_path, symbol)
		)
	`); err != nil {
		return fmt.Errorf("create items table: %w", err)
	}

	// meta 表
	if _, err := cdb.db.Exec(`
		CREATE TABLE IF NOT EXISTS codebase_meta(
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create meta table: %w", err)
	}

	// 写入当前配置
	if err := writeMeta(cdb.db, "model_name", cdb.modelName); err != nil {
		return err
	}
	if err := writeMeta(cdb.db, "model_id", cdb.modelID); err != nil {
		return err
	}
	if err := writeMeta(cdb.db, "dimension", strconv.Itoa(cdb.dimension)); err != nil {
		return err
	}

	return nil
}

// dropTables 删除 vec0 和 items 表（保留 meta 表用于校验）
func (cdb *CodebaseDB) dropTables() error {
	if _, err := cdb.db.Exec("DROP TABLE IF EXISTS codebase_vec"); err != nil {
		return fmt.Errorf("drop vec table: %w", err)
	}
	if _, err := cdb.db.Exec("DROP TABLE IF EXISTS codebase_items"); err != nil {
		return fmt.Errorf("drop items table: %w", err)
	}
	return nil
}

// readMeta 从 codebase_meta 读取值
func readMeta(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM codebase_meta WHERE key=?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// writeMeta 写入 or 更新 codebase_meta
func writeMeta(db *sql.DB, key, value string) error {
	_, err := db.Exec(
		"INSERT OR REPLACE INTO codebase_meta (key, value) VALUES (?, ?)",
		key, value,
	)
	return err
}

// joinPlaceholders 拼接 SQL IN 子句的占位符
func joinPlaceholders(ps []string) string {
	var result strings.Builder
	for i, p := range ps {
		if i > 0 {
			result.WriteString(",")
		}
		result.WriteString(p)
	}
	return result.String()
}
