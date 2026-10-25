// Package outline 持久化代码结构大纲（顶层符号 + 起始行）。
//
// 生成时机：
//   - codebase 索引（/index）扫描到文件时预生成并落库；
//   - edit 写入成功后立即刷新该文件的大纲；
//   - 大文件 read 时若缓存缺失/过期，现场用 LSP 提取并补齐。
//
// 过期判断：记录文件 mtime(ns) + size，读取时与磁盘 stat 对比，任一不一致即视为过期
// （删除、改写、替换都会变 mtime；同一纳秒内的原地改写靠 size 兜底）。
//
// 存储：每个工作目录的 .alkaid0/outline.sqlite，与 codebase.sqlite 分离，
// 避免索引清理/重建影响大纲缓存，也避免与向量表的连接生命周期互相牵制。
package outline

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite" // 注册 "sqlite" database/sql 驱动
)

// Symbol 大纲里的一行符号（与 LSP SymbolResult 解耦，便于测试替换）。
type Symbol struct {
	Name      string
	Kind      string
	Signature string
	Line      uint32 // 1-based 起始行
}

const (
	// MaxSymbols 大纲最多列出的符号数，超出只提示剩余数量，避免大纲本身又变成大文件。
	MaxSymbols = 160
	// maxSignatureLines 单个符号签名最多占用的行数（Go 的多行参数列表很常见）。
	maxSignatureLines = 4
)

// Record 一条已落库的大纲。
type Record struct {
	Text      string    // 渲染后的大纲正文（不含读取层头部）
	BuiltAt   time.Time // 大纲生成时间
	FileMTime time.Time // 生成时文件的修改时间
	Size      int64
}

var (
	dbMu sync.Mutex
	dbs  = map[string]*sql.DB{}
)

// db 打开（并按需创建）目录专属的大纲库。
func db(workdir string) (*sql.DB, error) {
	dir := filepath.Join(workdir, ".alkaid0")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("outline: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "outline.sqlite")

	dbMu.Lock()
	defer dbMu.Unlock()
	if d, ok := dbs[path]; ok {
		return d, nil
	}
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("outline: open %s: %w", path, err)
	}
	// 单连接串行化：写入只发生在索引/edit，读多写少；避免并发 PRAGMA 交错。
	d.SetMaxOpenConns(1)
	if _, err := d.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;`); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("outline: pragma: %w", err)
	}
	if _, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS file_outlines(
			file_path TEXT PRIMARY KEY,
			mtime_ns  INTEGER NOT NULL,
			size      INTEGER NOT NULL,
			outline   TEXT NOT NULL,
			built_at  INTEGER NOT NULL
		)`); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("outline: create table: %w", err)
	}
	dbs[path] = d
	return d, nil
}

// CloseAll 关闭所有已打开的目录连接（进程退出或测试清理临时目录前调用）。
func CloseAll() {
	dbMu.Lock()
	defer dbMu.Unlock()
	for path, d := range dbs {
		_ = d.Close()
		delete(dbs, path)
	}
}

// Normalize 归一化相对路径，保证索引/read/edit 三个入口写读同一个 key。
func Normalize(relPath string) string {
	p := filepath.ToSlash(filepath.Clean(strings.TrimSpace(relPath)))
	p = strings.TrimPrefix(p, "./")
	return p
}

// absPathOf 由工作目录 + 相对路径得到磁盘路径。
func absPathOf(workdir, relPath string) string {
	if filepath.IsAbs(relPath) {
		return filepath.Clean(relPath)
	}
	return filepath.Join(workdir, filepath.FromSlash(relPath))
}

// Put 用当前磁盘 mtime/size 记录大纲；syms 为空或文件不可读时删除旧记录（避免留下过期大纲）。
// 返回写入后的记录（用于调用方直接渲染，省一次读库）。
func Put(workdir, relPath string, syms []Symbol) (Record, bool) {
	key := Normalize(relPath)
	if key == "" || workdir == "" {
		return Record{}, false
	}
	text := Render(syms)
	if text == "" {
		_ = Drop(workdir, key)
		return Record{}, false
	}
	st, err := os.Stat(absPathOf(workdir, key))
	if err != nil {
		_ = Drop(workdir, key)
		return Record{}, false
	}
	rec := Record{
		Text:      text,
		BuiltAt:   time.Now(),
		FileMTime: st.ModTime(),
		Size:      st.Size(),
	}
	d, err := db(workdir)
	if err != nil {
		return rec, false
	}
	if _, err := d.Exec(
		`INSERT INTO file_outlines(file_path, mtime_ns, size, outline, built_at) VALUES(?,?,?,?,?)
		 ON CONFLICT(file_path) DO UPDATE SET mtime_ns=excluded.mtime_ns, size=excluded.size,
		     outline=excluded.outline, built_at=excluded.built_at`,
		key, rec.FileMTime.UnixNano(), rec.Size, rec.Text, rec.BuiltAt.Unix(),
	); err != nil {
		return rec, false
	}
	return rec, true
}

// Get 读取未过期的大纲：记录存在且 mtime+size 与磁盘一致。
// 磁盘文件不存在或已变化时返回 ok=false（调用方回退现场提取/截断）。
func Get(workdir, relPath string) (Record, bool) {
	key := Normalize(relPath)
	if key == "" || workdir == "" {
		return Record{}, false
	}
	st, err := os.Stat(absPathOf(workdir, key))
	if err != nil {
		return Record{}, false
	}
	d, err := db(workdir)
	if err != nil {
		return Record{}, false
	}
	var (
		mtimeNS int64
		size    int64
		text    string
		builtAt int64
	)
	if err := d.QueryRow(
		`SELECT mtime_ns, size, outline, built_at FROM file_outlines WHERE file_path = ?`, key,
	).Scan(&mtimeNS, &size, &text, &builtAt); err != nil {
		return Record{}, false
	}
	if text == "" || mtimeNS != st.ModTime().UnixNano() || size != st.Size() {
		return Record{}, false
	}
	return Record{
		Text:      text,
		BuiltAt:   time.Unix(builtAt, 0),
		FileMTime: st.ModTime(),
		Size:      size,
	}, true
}

// Cached 只判断「有未过期的大纲」而不取内容（索引增量跳过时用）。
func Cached(workdir, relPath string) bool {
	_, ok := Get(workdir, relPath)
	return ok
}

// Drop 删除某路径的大纲记录（文件被删除、符号全部消失或提取失败时）。
func Drop(workdir, relPath string) error {
	key := Normalize(relPath)
	if key == "" || workdir == "" {
		return nil
	}
	d, err := db(workdir)
	if err != nil {
		return err
	}
	_, err = d.Exec(`DELETE FROM file_outlines WHERE file_path = ?`, key)
	return err
}

// Render 按起始行排序渲染符号；超过 maxSymbols 只提示剩余数量。无可用符号返回空串。
func Render(syms []Symbol) string {
	ordered := make([]Symbol, 0, len(syms))
	seen := make(map[string]struct{}, len(syms))
	for _, s := range syms {
		if strings.TrimSpace(s.Name) == "" {
			continue
		}
		key := fmt.Sprintf("%d/%s", s.Line, s.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, s)
	}
	if len(ordered) == 0 {
		return ""
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Line < ordered[j].Line })

	var b strings.Builder
	for i, s := range ordered {
		if i >= MaxSymbols {
			fmt.Fprintf(&b, "... (%d more symbols)\n", len(ordered)-i)
			break
		}
		kind := s.Kind
		if kind == "" {
			kind = "symbol"
		}
		fmt.Fprintf(&b, "L%d\t%s\t%s\n", s.Line, kind, compactSignature(s))
	}
	return b.String()
}

// compactSignature 把签名压成一行：括号未闭合时续接下一行，直到括号平衡或达到行数上限；
// 没有签名时退回符号名。LSP 会把 hover 文档注释接在签名后面，括号平衡后立即停止即可切掉。
func compactSignature(s Symbol) string {
	if strings.TrimSpace(s.Signature) == "" {
		return s.Name
	}
	lines := strings.Split(s.Signature, "\n")
	var b strings.Builder
	depth := 0
	for i, ln := range lines {
		if i >= maxSignatureLines {
			break
		}
		b.WriteString(ln)
		b.WriteByte(' ')
		depth += strings.Count(ln, "(") - strings.Count(ln, ")")
		if depth <= 0 {
			break
		}
	}
	sig := strings.Join(strings.Fields(b.String()), " ")
	if sig == "" {
		return s.Name
	}
	return sig
}
