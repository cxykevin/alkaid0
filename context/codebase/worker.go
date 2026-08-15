package codebase

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"runtime"

	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/provider/request/structs"
)

// startWorker 启动当前目录的 worker goroutine（如果尚未运行）
func (cdb *DB) startWorker() {
	cdb.mu.Lock()
	defer cdb.mu.Unlock()

	if cdb.workerCancel != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	cdb.workerCtx = ctx
	cdb.workerCancel = cancel
	cdb.workerWG.Add(1)
	go cdb.worker(ctx)
}

// stopWorker 停止当前目录的 worker，等待当前任务完成
func (cdb *DB) stopWorker() {
	cdb.mu.Lock()
	cancel := cdb.workerCancel
	cdb.mu.Unlock()

	if cancel != nil {
		cancel()
		cdb.workerWG.Wait()

		cdb.mu.Lock()
		cdb.workerCancel = nil
		cdb.workerCtx = nil
		cdb.mu.Unlock()
	}
}

// worker 单个目录的嵌入任务处理 goroutine
func (cdb *DB) worker(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			cdb.logger.Error("worker panic: %v", r)
			buf := make([]byte, 4096)
			n := runtime.Stack(buf, false)
			cdb.logger.Error("stack: %s", string(buf[:n]))

			if ctx.Err() == nil {
				// 先清除旧的 worker 状态再重启，否则 startWorker 会因
				// workerCancel 仍非 nil 而直接返回，导致 worker 永久死亡、队列停摆。
				cdb.mu.Lock()
				cdb.workerCancel = nil
				cdb.workerCtx = nil
				cdb.mu.Unlock()
				cdb.startWorker()
			}
		}
		cdb.workerWG.Done()
	}()

	cdb.logger.Info("worker started")
	defer cdb.logger.Info("worker stopped")

	for {
		task := cdb.queue.WaitPop(ctx)
		if task == nil {
			return
		}

		if err := cdb.embedAndStore(ctx, task); err != nil {
			cdb.logger.Error("embed %s:%s: %v", task.FilePath, task.Symbol, err)
		}

		if task.Done != nil {
			close(task.Done)
		}
	}
}

// embedAndStore 执行嵌入计算并存储结果到数据库
func (cdb *DB) embedAndStore(ctx context.Context, task *EmbedTask) error {
	hash := embedHash(task.EmbedText)

	// 检查是否已有相同 hash 的记录，避免重复嵌入
	existingHash, err := cdb.checkExistingHash(task.FilePath, task.Symbol)
	if err != nil {
		return fmt.Errorf("check hash: %w", err)
	}
	if existingHash == hash {
		// hash 相同：跳过 API 和向量写入，但更新元数据（full_content, tags）
		cdb.logger.Debug("skip %s:%s (hash unchanged)", task.FilePath, task.Symbol)
		return cdb.updateMetadata(task.FilePath, task.Symbol, task.FullContent, task.Tags)
	}

	// 无 embedding 模型（BM25-only 模式）：仅入库内容，不嵌入、不存向量。
	// 这样即使未配置 embedding 模型，代码库的 BM25 全文检索依然可用。
	if cdb.modelID == "" {
		if _, err := cdb.upsertItem(task, hash); err != nil {
			return err
		}
		cdb.logger.Info("stored (bm25 only) %s:%s", task.FilePath, task.Symbol)
		return nil
	}

	cdb.logger.Info("embedding %s:%s (%d chars)", task.FilePath, task.Symbol, len(task.EmbedText))

	// 调用嵌入 API
	req := structs.EmbeddingRequest{
		Input: []string{task.EmbedText},
		Model: cdb.modelID,
	}
	embeddings, err := request.SimpleOpenAIEmbedding(ctx, cdb.providerURL, cdb.providerKey, cdb.modelID, req)
	if err != nil {
		return fmt.Errorf("api call: %w", err)
	}
	if len(embeddings) == 0 {
		return fmt.Errorf("api returned empty embeddings")
	}

	// 存入数据库
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

	tagsJSON := tagsToJSON(task.Tags)

	res, err := tx.Exec(`
		INSERT INTO codebase_items (file_path, symbol, tags, full_content, embed_text, embed_hash)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_path, symbol) DO UPDATE SET
			tags=excluded.tags,
			full_content=excluded.full_content,
			embed_text=excluded.embed_text,
			embed_hash=excluded.embed_hash,
			updated_at=CURRENT_TIMESTAMP
	`, task.FilePath, task.Symbol, tagsJSON, task.FullContent, task.EmbedText, hash)
	if err != nil {
		return fmt.Errorf("upsert items: %w", err)
	}

	itemID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	// 先删除旧向量（如果是更新），再插入新向量
	_, _ = tx.Exec("DELETE FROM codebase_vec WHERE id=?", itemID)

	vecBytes := float32SliceToBytes(embeddings[0])
	if _, err := tx.Exec(
		"INSERT INTO codebase_vec (id, embedding) VALUES (?, ?)",
		itemID, vecBytes,
	); err != nil {
		return fmt.Errorf("insert vec: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	cdb.logger.Info("stored %s:%s id=%d dim=%d", task.FilePath, task.Symbol, itemID, len(embeddings[0]))
	return nil
}

// upsertItem 插入或更新 codebase_items 记录（FTS5 触发器自动同步全文索引），返回记录 ID。
// 供 BM25-only 模式（无 embedding 模型）与完整嵌入流程共用。
func (cdb *DB) upsertItem(task *EmbedTask, hash string) (int64, error) {
	cdb.mu.Lock()
	defer cdb.mu.Unlock()

	if err := cdb.ensureDBOpen(); err != nil {
		return 0, err
	}

	tagsJSON := tagsToJSON(task.Tags)

	res, err := cdb.db.Exec(`
		INSERT INTO codebase_items (file_path, symbol, tags, full_content, embed_text, embed_hash)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_path, symbol) DO UPDATE SET
			tags=excluded.tags,
			full_content=excluded.full_content,
			embed_text=excluded.embed_text,
			embed_hash=excluded.embed_hash,
			updated_at=CURRENT_TIMESTAMP
	`, task.FilePath, task.Symbol, tagsJSON, task.FullContent, task.EmbedText, hash)
	if err != nil {
		return 0, fmt.Errorf("upsert items: %w", err)
	}

	itemID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return itemID, nil
}

// checkExistingHash 检查指定文件+符号是否已存在并返回其 hash
func (cdb *DB) checkExistingHash(filePath, symbol string) (string, error) {
	if err := cdb.ensureDBOpen(); err != nil {
		return "", err
	}

	var hash string
	err := cdb.db.QueryRow(
		"SELECT embed_hash FROM codebase_items WHERE file_path=? AND symbol=?",
		filePath, symbol,
	).Scan(&hash)

	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

// updateMetadata 更新指定文件+符号的元数据和更新时间戳（hash 未变时调用）
func (cdb *DB) updateMetadata(filePath, symbol, fullContent string, tags []string) error {
	cdb.mu.Lock()
	defer cdb.mu.Unlock()

	if err := cdb.ensureDBOpen(); err != nil {
		return err
	}

	tagsJSON := tagsToJSON(tags)
	_, err := cdb.db.Exec(
		`UPDATE codebase_items SET full_content=?, tags=?, updated_at=CURRENT_TIMESTAMP
		 WHERE file_path=? AND symbol=?`,
		fullContent, tagsJSON, filePath, symbol,
	)
	return err
}

// float32SliceToBytes 将 float32 向量编码为 little-endian 字节序列（sqlite-vec 格式）
func float32SliceToBytes(vec []float32) []byte {
	b := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(v))
	}
	return b
}
