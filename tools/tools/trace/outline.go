package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/context/outline"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// 大文件读取策略（docs/trace-cache-spec.md §11）：
//
//   - 小文件：始终注入全量。
//   - 大文件首读：优先注入「结构大纲」（顶层符号 + 起始行，由 LSP 提取）；提取失败
//     （没有语言服务器 / 该语言不支持 / 文件里没有顶层符号）则注入「截断摘要」（前 N 行）。
//   - 同一文件再次 read：升级为全量。受限视图与全文都会在块内提示模型用 search 定位。
//
// 受限视图的收益：大文件首读从几万 token 降到几百 token；块内容仍然稳定（大纲随文件
// 变化才变），不会破坏前缀缓存。
const (
	// readLightMinLines / readLightMinBytes 达到任一阈值即视为「大文件」。
	readLightMinLines = 400
	readLightMinBytes = 16 * 1024

	// outlineMaxSymbols 大纲最多列出的符号数，超出只提示剩余数量，
	// 避免大纲本身又变成大文件。
	outlineMaxSymbols = outline.MaxSymbols

	// 提不出大纲时的截断视图：保留前 N 行 / 最多 M 字节（先到者为准）。
	excerptKeepLines = 200
	excerptKeepBytes = 24 * 1024

	// lightViewMarker 受限视图的首行标记。渲染侧据此识别「该块自带格式，不要再加行号」，
	// 也用于判定上一次注入的是大纲还是截断（diff 旧块字节保持一致）。
	lightViewMarker = "[Alkaid0 large-file"

	// ReadModeLight 标记该 path 的首读只注入了受限视图；下一次 read 会升级为全量。
	// 空串 = 全量（默认）。
	ReadModeLight = "light"
)

// OutlineSymbol 大纲里的一行符号（别名到 context/outline.Symbol）。
// 由 LSP 层注入（trace 包不直接依赖 context/lsp），便于测试替换实现。
type OutlineSymbol = outline.Symbol

// outlineProvider 符号提取实现，由 ui/startup 注入（lsp.GetSymbols 的薄包装）。
// 未注入、报错或返回空时回退到截断视图。
var outlineProvider func(workdir, absPath string) ([]OutlineSymbol, error)

// SetOutlineProvider 注册符号提取实现（nil = 关闭大纲能力，始终走截断）。
func SetOutlineProvider(fn func(workdir, absPath string) ([]OutlineSymbol, error)) {
	outlineProvider = fn
}

// FileIndexSymbol 单文件索引刷新的符号分块。字段与 codebase.FileSymbol 一致，
// 但定义在 trace 侧——trace/tools 不能直接 import codebase（存在包依赖环）。
type FileIndexSymbol struct {
	Name      string
	Kind      string
	Signature string
	Code      string
}

// fileIndexRefresher 带符号的单文件刷新实现（startup 注入 codebase.RefreshFile）。
// edit 复用已经提取好的符号，避免同一文件第二次 LSP documentSymbol。
var fileIndexRefresher func(workdir, relPath, content string, symbols []FileIndexSymbol) error

// fileIndexRefresherFromDisk 无符号、从磁盘重建的实现（startup 注入
// codebase.RefreshFileFromDisk），供 read 现场重建大纲时使用。
var fileIndexRefresherFromDisk func(workdir, relPath string) error

// SetFileIndexRefresher 注册「带符号列表」的单文件刷新实现。
func SetFileIndexRefresher(fn func(workdir, relPath, content string, symbols []FileIndexSymbol) error) {
	fileIndexRefresher = fn
}

// SetFileIndexRefresherFromDisk 注册「从磁盘读取 + 提取符号」的单文件刷新实现。
func SetFileIndexRefresherFromDisk(fn func(workdir, relPath string) error) {
	fileIndexRefresherFromDisk = fn
}

// RefreshFileIndexWithSymbols 用调用方已提取的符号刷新索引：BM25 同步更新、
// 向量挂后台，不再重复跑 LSP。未注入实现时静默跳过。
func RefreshFileIndexWithSymbols(workdir, relPath, content string, symbols []FileIndexSymbol) {
	if fileIndexRefresher == nil {
		return
	}
	if err := fileIndexRefresher(workdir, relPath, content, symbols); err != nil {
		logger.Warn("refresh codebase index for %q failed: %v", relPath, err)
	}
}

// RefreshFileIndex 从磁盘重建单文件索引（read 发现大纲缺失/过期时使用）。
func RefreshFileIndex(workdir, relPath string) {
	if fileIndexRefresherFromDisk == nil {
		return
	}
	if err := fileIndexRefresherFromDisk(workdir, relPath); err != nil {
		logger.Warn("refresh codebase index for %q failed: %v", relPath, err)
	}
}

// isLargeFileContent 判断内容是否达到「大文件」阈值。
func isLargeFileContent(content string) bool {
	if len(content) >= readLightMinBytes {
		return true
	}
	return strings.Count(content, "\n")+1 >= readLightMinLines
}

// lightViewEntry 会话级缓存：同一文件在 size+mtime 未变时复用上次生成的大纲/截断文本，
// 避免每轮构建上下文都去问一次 LSP（GetSymbols 会对每个符号发 hover 请求）。
type lightViewEntry struct {
	size int64
	mod  int64
	text string
}

// injectReadView 生成真正写进上下文的文本。
// mode 不是 ReadModeLight（或内容为空）时原样返回全文。
func injectReadView(session *structs.Chats, workdir, path, mode, content string) string {
	if mode != ReadModeLight || content == "" {
		return content
	}
	abs := filepath.Join(workdir, path)
	if resolved, err := filepath.Abs(abs); err == nil {
		abs = resolved
	}
	var size, mod int64
	if st, err := os.Stat(abs); err == nil {
		size, mod = st.Size(), st.ModTime().UnixNano()
	}
	if text, ok := getLightViewCache(session, path, size, mod); ok {
		return text
	}
	// 大纲库以 session.Root 为工作目录、相对 Root 的路径为 key（与 codebase 索引一致）；
	// 注入内容按模型视角的 path 读取。
	text := buildLightView(session.Root, abs, outlineKey(session, path), path, content)
	setLightViewCache(session, path, size, mod, text)
	return text
}

// outlineKey 把模型视角路径（相对子代理绑定目录）换算成相对 session.Root 的 key，
// 与 codebase 索引写入的 filePath 保持一致；已含绑定目录前缀时不重复拼接。
func outlineKey(session *structs.Chats, path string) string {
	act := strings.TrimSpace(filepath.ToSlash(filepath.Clean(session.CurrentActivatePath)))
	if act == "" || act == "." {
		return path
	}
	p := filepath.ToSlash(filepath.Clean(path))
	if strings.HasPrefix(p, act+"/") {
		return p
	}
	return act + "/" + p
}

// lightViewTimeLayout 缓存时间在上下文里的展示格式（本地时区）。
const lightViewTimeLayout = "2006-01-02 15:04:05"

// buildLightView 依次尝试「持久化大纲」→「现场 LSP 大纲」→「截断」，返回带标记的受限视图。
//
// 持久化大纲由 codebase 索引预生成、edit 写入后即时刷新，按文件 mtime+size 判过期；
// 命中时无需再跑 LSP（GetSymbols 会对每个符号发 hover，代价高）。
func buildLightView(workdir, abs, key, path, content string) string {
	if rec, ok := outline.Get(workdir, key); ok {
		return renderOutlineView(path, content, rec)
	}
	if syms, ok := extractOutline(abs, path); ok {
		// 现场提取成功后写回持久缓存：本文件后续轮次、以及重启后都不必再跑 LSP。
		if rec, ok := outline.Put(workdir, key, syms); ok {
			// 大纲刚重建，说明索引对该文件也已落后：同步刷新 BM25、向量挂后台。
			RefreshFileIndex(workdir, key)
			return renderOutlineView(path, content, rec)
		}
		now := time.Now()
		return renderOutlineView(path, content, outline.Record{
			Text:      outline.Render(syms),
			BuiltAt:   now,
			FileMTime: now,
		})
	}
	return buildExcerptView(path, content, mtimeStamp(workdir, key))
}

// extractOutline 调用注入的 LSP 提取实现；未注入/报错/无符号时 ok=false。
func extractOutline(abs, path string) ([]OutlineSymbol, bool) {
	if outlineProvider == nil {
		return nil, false
	}
	syms, err := outlineProvider(filepath.Dir(abs), abs)
	if err != nil {
		logger.Warn("outline for %q unavailable, fallback to excerpt: %v", path, err)
		return nil, false
	}
	return syms, len(syms) > 0
}

// renderOutlineView 渲染大纲块：header（含缓存时间）+ 大纲正文。
func renderOutlineView(path, content string, rec outline.Record) string {
	return lightViewHeader("outline", path, content, outlineStamp(rec)) + "\n\n" + rec.Text
}

// outlineStamp 给模型判断「大纲是否过期」的时间信息：生成时间 + 文件修改时间。
func outlineStamp(rec outline.Record) string {
	return fmt.Sprintf(
		"Outline cached at %s; file mtime %s. If the file was modified after the cache time, call read again (or search) for the current code.",
		rec.BuiltAt.Format(lightViewTimeLayout), rec.FileMTime.Format(lightViewTimeLayout))
}

// mtimeStamp 截断视图的时间提示：文件最后修改时间。
func mtimeStamp(workdir, path string) string {
	if st, err := os.Stat(filepath.Join(workdir, path)); err == nil {
		return fmt.Sprintf("File mtime %s.", st.ModTime().Format(lightViewTimeLayout))
	}
	return ""
}

// lightViewHeader 受限视图头部；stamp 非空时追加一行时间信息。
func lightViewHeader(kind, path string, content string, stamp string) string {
	head := fmt.Sprintf("%s %s] %s\n%d lines / %d bytes — the full content is NOT injected.\n",
		lightViewMarker, kind, path, strings.Count(content, "\n")+1, len(content))
	if stamp != "" {
		head += stamp + "\n"
	}
	return head + fmt.Sprintf(
		"Use the \"search\" tool (query = symbol name or keyword) to locate the exact code.\n"+
			"Call read {\"path\":%q} again to get the complete file.", path)
}

// renderOutline 渲染大纲正文（实现已下沉到 context/outline，包装保留给测试与内部复用）。
func renderOutline(syms []OutlineSymbol) string { return outline.Render(syms) }

// buildExcerptView 保留前 N 行 / M 字节，并在末尾说明省略了多少行。
func buildExcerptView(path, content, stamp string) string {
	kept, omitted := excerptOf(content)
	numbered, ok := renderContentBlock(path, kept)
	text := ""
	if ok {
		text = numbered.Text
	} else {
		text = kept
	}
	if omitted > 0 {
		text += fmt.Sprintf("... (%d more lines omitted)\n", omitted)
	}
	return lightViewHeader("excerpt", path, content, stamp) + "\n\n" + text
}

// excerptOf 截取内容前缀：最多 excerptKeepLines 行，且不超过 excerptKeepBytes 字节。
func excerptOf(content string) (string, int) {
	lines := strings.Split(content, "\n")
	var b strings.Builder
	used, kept := 0, 0
	for i, ln := range lines {
		if i >= excerptKeepLines {
			break
		}
		add := len(ln) + 1
		if kept > 0 && used+add > excerptKeepBytes {
			break
		}
		b.WriteString(ln)
		b.WriteByte('\n')
		used += add
		kept = i + 1
	}
	return strings.TrimSuffix(b.String(), "\n"), len(lines) - kept
}

// lightViewKind 识别受限视图的类型（"outline" / "excerpt"）；非受限视图返回空串。
func lightViewKind(content string) string {
	if !strings.HasPrefix(content, lightViewMarker) {
		return ""
	}
	switch {
	case strings.HasPrefix(content, lightViewMarker+" outline]"):
		return "outline"
	case strings.HasPrefix(content, lightViewMarker+" excerpt]"):
		return "excerpt"
	}
	return "outline"
}

func getLightViewCache(session *structs.Chats, path string, size, mod int64) (string, bool) {
	if session == nil || session.TemporyDataOfSession == nil {
		return "", false
	}
	cache, _ := session.TemporyDataOfSession[structs.TempKeyTraceOutline].(map[string]lightViewEntry)
	if cache == nil {
		return "", false
	}
	e, ok := cache[path]
	if !ok || e.size != size || e.mod != mod || e.text == "" {
		return "", false
	}
	return e.text, true
}

func setLightViewCache(session *structs.Chats, path string, size, mod int64, text string) {
	if session == nil || text == "" {
		return
	}
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	cache, _ := session.TemporyDataOfSession[structs.TempKeyTraceOutline].(map[string]lightViewEntry)
	if cache == nil {
		cache = make(map[string]lightViewEntry)
		session.TemporyDataOfSession[structs.TempKeyTraceOutline] = cache
	}
	cache[path] = lightViewEntry{size: size, mod: mod, text: text}
}

// confirmInjectedContent 记录本轮实际注入上下文的字节（可能是大纲/截断视图）。
// 与 confirmTraceContent（真实文件内容，供 edit 外部修改校验）分开维护：
// diff 基线必须等于实际注入的字节，否则方案2 的旧块会与上一轮注入的不一致。
func confirmInjectedContent(session *structs.Chats, path, text string) {
	if session == nil || path == "" {
		return
	}
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	confirmed, _ := session.TemporyDataOfSession[structs.TempKeyTraceInjected].(map[string]string)
	if confirmed == nil {
		confirmed = make(map[string]string)
		session.TemporyDataOfSession[structs.TempKeyTraceInjected] = confirmed
	}
	confirmed[normalizeTraceContentKey(path)] = text
}

// injectedContent 取本轮实际注入的字节。
func injectedContent(session *structs.Chats, path string) (string, bool) {
	if session == nil || session.TemporyDataOfSession == nil {
		return "", false
	}
	confirmed, _ := session.TemporyDataOfSession[structs.TempKeyTraceInjected].(map[string]string)
	text, ok := confirmed[normalizeTraceContentKey(path)]
	return text, ok && text != ""
}
