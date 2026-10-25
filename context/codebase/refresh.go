package codebase

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxykevin/alkaid0/context/lsp"
)

// FileSymbol 单文件刷新时的符号分块（Code 供向量/全文使用，Signature 用于嵌入文本）。
type FileSymbol struct {
	Name      string
	Kind      string
	Signature string
	Code      string
}

// RefreshFile 刷新单文件的代码索引：
//
//   - BM25 同步更新（阻塞）：直接 upsert codebase_items，FTS5 触发器立刻同步
//     codebase_fts，搜索马上能命中新内容；
//   - 向量挂后台：把任务丢进 embedding 队列，worker 异步调用 API 并写 vec0。
//
// BM25 写入时 embed_hash 故意置空，避免 worker 的 hash 比对把"内容没变"误判成
// 无需重算——这里的目标正是让向量跟随文件变化。
//
// 刷新期间持文件写锁（见 filelock.go）：RunIndex 对同一文件会跳过，不会用扫描阶段
// 的旧内容覆盖刚写入的新数据。
func RefreshFile(cwd, relPath, content string, symbols []FileSymbol) error {
	if cwd == "" || relPath == "" {
		return nil
	}
	unlock, seq := lockFileForRefresh(fileKey(cwd, relPath))
	defer unlock()

	// 与 RunIndex 相同的按类型截断规则，保证两个入口写入的全文一致。
	// 无扩展名文件（Makefile 等）同样走文件名映射，避免两个入口用不同截断策略。
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == "" {
		if mapped, ok := lsp.GetFileNameExt(filepath.Base(relPath)); ok {
			ext = mapped
		}
	}
	content = string(truncateContent(ext, []byte(content)))

	cdb, err := getOrCreateDB(cwd)
	if err != nil {
		return fmt.Errorf("refresh file: %w", err)
	}

	active := make([]string, 0, len(symbols))
	seen := make(map[string]struct{}, len(symbols))
	for _, s := range symbols {
		if s.Name == "" {
			continue
		}
		if _, ok := seen[s.Name]; ok {
			continue
		}
		seen[s.Name] = struct{}{}
		active = append(active, s.Name)
	}

	// 先写新内容（FTS 触发器同步 BM25），再清理已消失的符号：
	// 反序会让"旧条目已删、新条目未写"的空窗暴露给并发搜索。
	tasks := make([]EmbedTask, 0, len(symbols)+1)
	for _, s := range symbols {
		if s.Name == "" {
			continue
		}
		embedText := s.Signature
		if embedText == "" {
			embedText = s.Code
		}
		tags := []string{"file"}
		if s.Kind != "" {
			tags = []string{s.Kind}
		}
		task := EmbedTask{
			EmbedText:   embedText,
			FullContent: s.Code,
			FilePath:    relPath,
			Symbol:      s.Name,
			Tags:        tags,
			Seq:         seq,
		}
		if _, err := cdb.upsertItem(&task, ""); err != nil {
			return fmt.Errorf("refresh file bm25 %s: %w", s.Name, err)
		}
		tasks = append(tasks, task)
	}

	// 整文件条目（与索引一致：Symbol=""，tags=["file"]），保证全局语义/全文兜底最新。
	fileTask := EmbedTask{
		EmbedText:   content,
		FullContent: content,
		FilePath:    relPath,
		Symbol:      "",
		Tags:        []string{"file"},
		Seq:         seq,
	}
	if _, err := cdb.upsertItem(&fileTask, ""); err != nil {
		return fmt.Errorf("refresh file bm25: %w", err)
	}
	tasks = append(tasks, fileTask)

	if err := CleanSymbols(cwd, relPath, active); err != nil {
		return fmt.Errorf("refresh file clean symbols: %w", err)
	}

	// 向量后台：入队即返回；worker 发现 hash 为空会重新嵌入并覆盖 vec0。
	for i := range tasks {
		_ = AddToQueue(cwd, tasks[i])
	}
	return nil
}

// RefreshFileFromDisk 从磁盘读取文件后刷新大纲之外的代码索引（read 发现大纲缺失/
// 过期时使用：此时文件内容与符号都需要重建）。
func RefreshFileFromDisk(cwd, relPath string) error {
	abs := filepath.Join(cwd, filepath.FromSlash(relPath))
	raw, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	symbols, _ := lsp.GetSymbols(cwd, abs)
	return RefreshFile(cwd, relPath, string(raw), toFileSymbols(symbols))
}

// toFileSymbols 把 LSP 符号结果转为索引刷新需要的分块。
func toFileSymbols(symbols []lsp.SymbolResult) []FileSymbol {
	out := make([]FileSymbol, 0, len(symbols))
	for _, sym := range symbols {
		out = append(out, FileSymbol{
			Name:      sym.Name,
			Kind:      sym.KindName,
			Signature: sym.Signature,
			Code:      sym.Code,
		})
	}
	return out
}
