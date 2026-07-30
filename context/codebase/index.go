package codebase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/context/lsp"
)

// ---------------------------------------------------------------------------
// 索引进度状态（向前端广播）
// ---------------------------------------------------------------------------

// IndexStatus 索引进度状态
type IndexStatus struct {
	Total       int    `json:"total"`
	Processed   int    `json:"processed"`
	Remaining   int    `json:"remaining"`
	CurrentFile string `json:"currentFile"`
	Status      string `json:"status"` // "scanning" | "indexing" | "completed" | "error"
	Error       string `json:"error,omitempty"`
}

// indexingLocks 防止同一目录并发索引
var indexingLocks = make(map[string]bool)
var indexingLocksMu sync.Mutex

// indexCancels 存储每个正在索引目录的取消函数
var indexCancels = make(map[string]context.CancelFunc)
var indexCancelsMu sync.Mutex

// currentIndexStatuses 存储每个目录的最新索引进度
var currentIndexStatuses = make(map[string]IndexStatus)
var currentIndexStatusesMu sync.Mutex

// GetIndexStatus 返回指定目录当前索引进度，nil 表示未在索引
func GetIndexStatus(directory string) *IndexStatus {
	absPath, err := filepath.Abs(directory)
	if err != nil {
		return nil
	}
	currentIndexStatusesMu.Lock()
	defer currentIndexStatusesMu.Unlock()
	status, ok := currentIndexStatuses[absPath]
	if !ok {
		return nil
	}
	return &status
}

// CancelIndex 取消指定目录正在进行的索引
func CancelIndex(directory string) error {
	absPath, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	indexCancelsMu.Lock()
	defer indexCancelsMu.Unlock()
	if cancel, ok := indexCancels[absPath]; ok {
		cancel()
		delete(indexCancels, absPath)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 跳过规则
// ---------------------------------------------------------------------------

// skipDirs 始终跳过的目录名
var skipDirs = map[string]bool{
	".git":         true,
	".alkaid0":     true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"target":       true,
	"vendor":       true,
	".next":        true,
	".nuxt":        true,
	".svelte-kit":  true,
	"dist":         true,
	"build":        true,
	"bin":          true,
	"obj":          true,
	".svn":         true,
	".hg":          true,
	".tox":         true,
	".eggs":        true,
	"eggs":         true,
	".cache":       true,
	".npm":         true,
	".yarn":        true,
	".bundle":      true,
	".serverless":  true,
	".terraform":   true,
	"third_party":  true,
}

// privacyFiles 隐私文件名（精确匹配）
var privacyFiles = map[string]bool{
	".env":             true,
	".env.local":       true,
	".env.production":  true,
	".env.development": true,
	".env.example":     true,
}

// privacyExts 隐私文件扩展名
var privacyExts = map[string]bool{
	".pem":    true,
	".key":    true,
	".cert":   true,
	".crt":    true,
	".pub":    true,
	".gpg":    true,
	".pgp":    true,
	".secret": true,
}

// privacyNamePrefixes 隐私文件名前缀
var privacyNamePrefixes = []string{
	"id_rsa",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"credentials",
	"secret",
	"oauth",
}

// ---------------------------------------------------------------------------
// gitignore 解析与匹配（手动实现，不依赖 git 命令）
// ---------------------------------------------------------------------------

// gitignorePattern 单条 gitignore 规则
type gitignorePattern struct {
	pattern  string
	negate   bool // 以 ! 开头
	dirOnly  bool // 以 / 结尾，仅匹配目录
	rooted   bool // 以 / 开头，从根目录匹配
	hasSlash bool // 模式中包含 /
}

// loadGitignore 加载 .gitignore 文件
func loadGitignore(dir string) []gitignorePattern {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	return parseGitignore(string(data))
}

// parseGitignore 解析 .gitignore 内容为规则列表
func parseGitignore(content string) []gitignorePattern {
	lines := strings.Split(content, "\n")
	patterns := make([]gitignorePattern, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 处理尾部空格转义（\\ 保留空格）
		if strings.HasSuffix(line, "\\ ") {
			line = line[:len(line)-2] + " "
		}

		negate := strings.HasPrefix(line, "!")
		if negate {
			line = line[1:]
		}

		dirOnly := strings.HasSuffix(line, "/")
		if dirOnly {
			line = strings.TrimSuffix(line, "/")
		}

		rooted := strings.HasPrefix(line, "/")
		if rooted {
			line = line[1:]
		}

		hasSlash := strings.Contains(line, "/")

		// 转义 filepath.Match 中的特殊字符保持字面意义
		// gitignore 语法中只有 * ? [ ] 是通配符，其余需转义
		patterns = append(patterns, gitignorePattern{
			pattern:  line,
			negate:   negate,
			dirOnly:  dirOnly,
			rooted:   rooted,
			hasSlash: hasSlash,
		})
	}
	return patterns
}

// matchGitignore 判断路径是否匹配 gitignore 规则
func matchGitignore(patterns []gitignorePattern, path string, isDir bool) bool {
	matched := false
	for _, p := range patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if matchPattern(p, normalizePath(path)) {
			if p.negate {
				return false
			}
			matched = true
		}
	}
	return matched
}

// normalizePath 将路径统一为使用 / 分隔符，移除末尾 /
func normalizePath(path string) string {
	path = filepath.ToSlash(path)
	path = strings.TrimSuffix(path, "/")
	return path
}

// matchPattern 判断路径是否匹配单条 gitignore 模式
func matchPattern(p gitignorePattern, path string) bool {
	pattern := p.pattern

	// 处理 ** 通配符
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, path)
	}

	if !p.hasSlash && !p.rooted {
		// 无斜杠且不以 / 开头：匹配文件名或任意路径段
		_, name := filepath.Split(path)
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
		// 也检查路径的每个段
		for part := range strings.SplitSeq(path, "/") {
			if ok, _ := filepath.Match(pattern, part); ok {
				return true
			}
		}
		return false
	}

	if p.rooted {
		// 以 / 开头：从根目录匹配
		if path == pattern {
			return true
		}
		if strings.HasPrefix(path, pattern+"/") {
			return true
		}
		// filepath.Match 也尝试一次
		if ok, _ := filepath.Match(pattern, path); ok {
			return true
		}
		return false
	}

	// 包含斜杠（非根）：匹配完整路径或后缀
	if ok, _ := filepath.Match(pattern, path); ok {
		return true
	}
	if path == pattern {
		return true
	}
	if strings.HasSuffix(path, "/"+pattern) {
		return true
	}
	return false
}

// matchDoubleStar 处理包含 ** 的 gitignore 模式
func matchDoubleStar(pattern, path string) bool {
	// ** 单独：匹配所有
	if pattern == "**" {
		return true
	}

	// **/foo：在任何层级匹配 foo
	if strings.HasPrefix(pattern, "**/") {
		suffix := pattern[3:]
		if strings.HasSuffix(path, "/"+suffix) || path == suffix {
			return true
		}
		if ok, _ := filepath.Match(suffix, filepath.Base(path)); ok {
			return true
		}
		// 检查每一段
		for part := range strings.SplitSeq(path, "/") {
			if ok, _ := filepath.Match(suffix, part); ok {
				return true
			}
		}
		return false
	}

	// a/**/b：a 和 b 之间的任意层级
	if strings.Contains(pattern, "/**/") {
		parts := strings.SplitN(pattern, "/**/", 2)
		prefix, suffix := parts[0], parts[1]
		if !strings.HasPrefix(path, prefix+"/") {
			return false
		}
		tail := path[len(prefix)+1:]
		if strings.HasSuffix(tail, suffix) || strings.HasSuffix(tail, "/"+suffix) {
			return true
		}
		if ok, _ := filepath.Match(suffix, filepath.Base(tail)); ok {
			return true
		}
		return false
	}

	// foo/**：匹配 foo 下所有内容
	if strings.HasSuffix(pattern, "/**") {
		prefix := pattern[:len(pattern)-3]
		if path == prefix {
			return true
		}
		return strings.HasPrefix(path, prefix+"/")
	}

	return false
}

// ---------------------------------------------------------------------------
// 文件过滤
// ---------------------------------------------------------------------------

// getWhitelistExts 从 LSP 配置中获取白名单扩展名集
func getWhitelistExts() map[string]bool {
	exts := lsp.SupportedExtensions()
	m := make(map[string]bool, len(exts))
	for _, ext := range exts {
		m[ext] = true
	}
	return m
}

// isBinary 检测数据是否为二进制（前 8KB 中是否有空字节）
func isBinary(data []byte) bool {
	const maxCheck = 8192
	checkLen := min(len(data), maxCheck)
	return slices.Contains(data[:checkLen], 0)
}

// isPrivacyFile 检查文件路径是否为隐私文件
func isPrivacyFile(path string) bool {
	base := filepath.Base(path)
	if privacyFiles[base] {
		return true
	}
	if privacyExts[filepath.Ext(base)] {
		return true
	}
	baseLower := strings.ToLower(base)
	for _, prefix := range privacyNamePrefixes {
		if strings.HasPrefix(baseLower, prefix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 主索引函数
// ---------------------------------------------------------------------------

// throttledBroadcast 包装 broadcastFn，对频繁的进度更新做 0.2s 限流。
// "scanning" / "completed" / "error" 等关键状态始终立即通过。
func throttledBroadcast(fn func(IndexStatus)) func(IndexStatus) {
	var mu sync.Mutex
	var last time.Time

	return func(s IndexStatus) {
		switch s.Status {
		case "scanning", "completed", "error":
			mu.Lock()
			fn(s)
			last = time.Now()
			mu.Unlock()
			return
		}

		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) >= 200*time.Millisecond {
			fn(s)
			last = time.Now()
		}
	}
}

// truncateContent 按文件类型限制索引内容行数
func truncateContent(ext string, content []byte) []byte {
	lines := bytes.Split(content, []byte{'\n'})
	switch ext {
	case ".txt":
		if len(lines) > 5 {
			lines = lines[:5]
		}
	case ".json":
		// 使用 JSON5 清洗后格式化（支持注释、结尾逗号、顶层数组/字符串）
		if cleaned := cleanJSON5(string(content)); cleaned != "" {
			var buf bytes.Buffer
			if err := json.Indent(&buf, []byte(cleaned), "", "  "); err == nil {
				content = buf.Bytes()
				lines = bytes.Split(content, []byte{'\n'})
			}
		}
		if len(lines) > 20 {
			lines = lines[:20]
		}
	case ".jsonl":
		// jsonl：取第一行格式化，取前10行，最大500字符
		firstLine := content
		if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
			firstLine = content[:idx]
		}
		if cleaned := cleanJSON5(string(firstLine)); cleaned != "" {
			var buf bytes.Buffer
			if err := json.Indent(&buf, []byte(cleaned), "", "  "); err == nil {
				formatted := buf.Bytes()
				formattedLines := bytes.Split(formatted, []byte{'\n'})
				if len(formattedLines) > 10 {
					formattedLines = formattedLines[:10]
				}
				out := bytes.Join(formattedLines, []byte{'\n'})
				if len(out) > 500 {
					out = out[:500]
				}
				lines = bytes.Split(out, []byte{'\n'})
			}
		}
	case ".toml", ".ini":
		// 用正则提取顶层配置 key=value 和 [section] 结构，每个 section 至多3行
		reKV := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9._-]*)\s*=`)
		var tomlLines [][]byte
		secLines := 0 // 当前 section 已保留的行数
		for _, raw := range lines {
			s := strings.TrimSpace(string(raw))
			if s == "" {
				continue
			}
			if strings.HasPrefix(s, "#") || strings.HasPrefix(s, ";") {
				tomlLines = append(tomlLines, []byte(s))
				continue
			}
			if strings.HasPrefix(s, "[") {
				secLines = 0
				tomlLines = append(tomlLines, []byte(s))
				continue
			}
			if reKV.MatchString(s) && secLines < 3 {
				secLines++
				tomlLines = append(tomlLines, []byte(s))
			}
		}
		if len(tomlLines) > 30 {
			tomlLines = tomlLines[:30]
		}
		lines = tomlLines
	case ".md", ".mdx":
		// Markdown/MDX：只提取 h1-h3 标题行（保留 # 前缀便于搜索区分层级）
		reHeading := regexp.MustCompile(`^#{1,3}\s`)
		var mdLines [][]byte
		for _, raw := range lines {
			s := string(raw)
			if reHeading.MatchString(s) {
				mdLines = append(mdLines, raw)
			}
		}
		if len(mdLines) > 30 {
			mdLines = mdLines[:30]
		}
		lines = mdLines
	case ".makefile":
		// Makefile：提取 target 定义行和第一个命令
		reTarget := regexp.MustCompile(`^[a-zA-Z0-9_.-]+:`)
		var mkLines [][]byte
		for _, raw := range lines {
			s := string(raw)
			if strings.HasPrefix(s, "\t") && len(mkLines) > 0 && len(mkLines) < 50 {
				mkLines = append(mkLines, raw)
				continue
			}
			if reTarget.MatchString(s) && len(mkLines) < 50 {
				mkLines = append(mkLines, raw)
			}
		}
		if len(mkLines) > 30 {
			mkLines = mkLines[:30]
		}
		lines = mkLines
	case ".dockerfile":
		// Dockerfile：提取指令行
		reDocker := regexp.MustCompile(`^(FROM|RUN|CMD|COPY|ADD|ENV|EXPOSE|ENTRYPOINT|LABEL|WORKDIR|ARG|VOLUME|USER|SHELL|STOPSIGNAL|HEALTHCHECK|MAINTAINER)\b`)
		var dfLines [][]byte
		for _, raw := range lines {
			if len(dfLines) >= 30 {
				break
			}
			s := strings.TrimSpace(string(raw))
			if reDocker.MatchString(s) {
				dfLines = append(dfLines, []byte(s))
			}
		}
		lines = dfLines
	case ".license":
		// LICENSE：只取前 5 行（标准许可证模板头）
		if len(lines) > 5 {
			lines = lines[:5]
		}
	case ".yaml", ".yml":
		if len(lines) > 20 {
			lines = lines[:20]
		}
	}
	return bytes.Join(lines, []byte{'\n'})
}

// cleanJSON5 将 JSON5 内容清洗为标准 JSON，移除注释和结尾逗号。
// 支持顶层对象 {}、数组 []、字符串 ""、数字等所有 JSON5 类型。
func cleanJSON5(input string) string {
	var out strings.Builder
	out.Grow(len(input))

	inStr := false     // 是否在字符串中
	strChar := byte(0) // 当前字符串界定符 ' 或 "
	escaped := false   // 是否刚遇到反斜杠

	// 多行注释状态
	inBlockComment := false
	// 单行注释状态（到行尾）
	inLineComment := false

	flush := func(b byte) {
		out.WriteByte(b)
	}

	for i := 0; i < len(input); i++ {
		ch := input[i]

		// ---- 字符串处理（优先于注释） ----
		if inBlockComment {
			if ch == '*' && i+1 < len(input) && input[i+1] == '/' {
				i++ // 跳过 /
				inBlockComment = false
			}
			continue
		}

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				flush('\n')
			}
			continue
		}

		if inStr {
			flush(ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && !escaped {
				escaped = true
				continue
			}
			if ch == strChar {
				inStr = false
			}
			continue
		}

		// ---- 注释检测（不在字符串内） ----
		if ch == '/' && i+1 < len(input) {
			next := input[i+1]
			if next == '/' {
				inLineComment = true
				i++ // 跳过第二个 /
				continue
			}
			if next == '*' {
				inBlockComment = true
				i++ // 跳过 *
				continue
			}
		}

		// ---- 字符串开始 ----
		if ch == '"' || ch == '\'' {
			inStr = true
			strChar = ch
			flush(ch)
			continue
		}

		// ---- 逗号处理 ----
		if ch == ',' {
			// 向前扫描（跳过空白），判断是否在 } 或 ] 前
			// 若是则跳过此逗号（清理结尾逗号）
			j := i + 1
			for j < len(input) {
				ws := input[j]
				if ws == ' ' || ws == '\t' || ws == '\n' || ws == '\r' {
					j++
					continue
				}
				break
			}
			if j < len(input) && (input[j] == '}' || input[j] == ']') {
				continue // 跳过结尾逗号
			}
		}

		flush(ch)
	}

	return out.String()
}

// RunIndex 扫描 cwd 下的合规文件，逐个提取 LSP 符号并提交嵌入任务。
// broadcastFn 可选，每次状态变更时调用以广播进度。
func RunIndex(ctx context.Context, cwd string, broadcastFn func(IndexStatus)) error {
	if broadcastFn == nil {
		broadcastFn = func(IndexStatus) {}
	}
	broadcastFn = throttledBroadcast(broadcastFn)

	whitelist := getWhitelistExts()
	if len(whitelist) == 0 {
		return fmt.Errorf("no supported extensions found (LSP not configured)")
	}

	// 新索引周期，重置 LSP 失败计数
	lsp.ResetLSPFailures()

	gi := loadGitignore(cwd)

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("abs cwd: %w", err)
	}

	// 创建可取消的 context 用于 /index cancel
	ctx, cancel := context.WithCancel(ctx)
	indexCancelsMu.Lock()
	indexCancels[absCwd] = cancel
	indexCancelsMu.Unlock()

	// 防止同一目录并发索引
	indexingLocksMu.Lock()
	if indexingLocks[absCwd] {
		indexingLocksMu.Unlock()
		return fmt.Errorf("index already running for %s", absCwd)
	}
	indexingLocks[absCwd] = true
	indexingLocksMu.Unlock()
	defer func() {
		indexCancelsMu.Lock()
		delete(indexCancels, absCwd)
		indexCancelsMu.Unlock()
		indexingLocksMu.Lock()
		delete(indexingLocks, absCwd)
		indexingLocksMu.Unlock()
		currentIndexStatusesMu.Lock()
		delete(currentIndexStatuses, absCwd)
		currentIndexStatusesMu.Unlock()
	}()

	// 包装 broadcastFn，同时保存最新状态供 /index status 查询
	innerFn := broadcastFn
	currentIndexStatusesMu.Lock()
	currentIndexStatuses[absCwd] = IndexStatus{Status: "scanning"}
	currentIndexStatusesMu.Unlock()
	broadcastFn = func(s IndexStatus) {
		currentIndexStatusesMu.Lock()
		currentIndexStatuses[absCwd] = s
		currentIndexStatusesMu.Unlock()
		innerFn(s)
	}

	// 读取已有的文件路径（增量的基数）
	existingPaths, _ := GetFilePaths(cwd) // 首次运行 DB 不存在时忽略错误
	existingPathSet := make(map[string]bool, len(existingPaths))
	for _, p := range existingPaths {
		existingPathSet[p] = true
	}
	scannedPaths := make(map[string]bool)

	// -----------------------------------------------------------------------
	// 第一遍：遍历目录，收集合规文件
	// -----------------------------------------------------------------------

	type fileInfo struct {
		path    string
		relPath string
		content []byte
	}
	var files []fileInfo

	walkErr := filepath.WalkDir(cwd, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // 跳过无法访问的路径
		}

		relPath, err := filepath.Rel(absCwd, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		if relPath == "." {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			// 跳过常见忽略目录和隐藏目录（. 和 .. 除外）
			if skipDirs[name] || (strings.HasPrefix(name, ".") && name != "." && name != "..") {
				return filepath.SkipDir
			}
			// gitignore 目录匹配
			if gi != nil && matchGitignore(gi, relPath+"/", true) {
				return filepath.SkipDir
			}
			return nil
		}

		// 1) 扩展名白名单（无扩展名文件通过文件名映射伪扩展名）
		ext := strings.ToLower(filepath.Ext(path))
		if ext == "" {
			if mapped, ok := lsp.GetFileNameExt(filepath.Base(path)); ok {
				ext = mapped
			}
		}
		if !whitelist[ext] {
			return nil
		}

		// 2) 隐私文件
		if isPrivacyFile(path) {
			return nil
		}

		// 3) gitignore 文件匹配
		if gi != nil && matchGitignore(gi, relPath, false) {
			return nil
		}

		// 4) 大小检查（> 2MB 跳过）
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > 2*1024*1024 {
			return nil
		}
		if info.Size() == 0 {
			return nil
		}

		// 5) 读取内容
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		// 6) 二进制检测
		if isBinary(content) {
			return nil
		}

		// 7) 内容截断：按文件类型限制索引行数
		content = truncateContent(ext, content)

		scannedPaths[relPath] = true
		files = append(files, fileInfo{
			path:    path,
			relPath: relPath,
			content: content,
		})
		return nil
	})
	if walkErr != nil {
		return fmt.Errorf("walk dir: %w", walkErr)
	}

	total := len(files)
	broadcastFn(IndexStatus{
		Total:  total,
		Status: "scanning",
	})

	// -----------------------------------------------------------------------
	// 第二遍：串行处理每个文件，提取符号并提交嵌入任务
	// -----------------------------------------------------------------------

	for i, f := range files {
		select {
		case <-ctx.Done():
			broadcastFn(IndexStatus{
				Status: "error",
				Error:  "cancelled",
			})
			return ctx.Err()
		default:
		}

		broadcastFn(IndexStatus{
			Total:       total,
			Processed:   i,
			Remaining:   total - i,
			CurrentFile: f.relPath,
			Status:      "indexing",
		})

		// 尝试 LSP 提取符号
		symbols, lspErr := lsp.GetSymbols(cwd, f.path)
		if lspErr != nil || len(symbols) == 0 {
			// LSP 不可用或文件无符号：索引整个文件（hash 未变则跳过）
			embedText := string(f.content)
			if same, _ := CheckContentHash(cwd, f.relPath, "", embedText); !same {
				_ = AddToQueue(cwd, EmbedTask{
					EmbedText:   embedText,
					FullContent: string(f.content),
					FilePath:    f.relPath,
					Symbol:      "",
					Tags:        []string{"file"},
				})
			}
			continue
		}

		// 提取活跃符号名列表
		activeSymbols := make([]string, 0, len(symbols))
		for _, sym := range symbols {
			activeSymbols = append(activeSymbols, sym.Name)
		}

		// 清理已删除的符号
		_ = CleanSymbols(cwd, f.relPath, activeSymbols)

		// 对每个符号创建嵌入任务（先查 hash，未变更则跳过）
		for _, sym := range symbols {
			embedText := sym.Signature
			if embedText == "" {
				embedText = sym.Code
			}
			if same, _ := CheckContentHash(cwd, f.relPath, sym.Name, embedText); !same {
				_ = AddToQueue(cwd, EmbedTask{
					EmbedText:   embedText,
					FullContent: sym.Code,
					FilePath:    f.relPath,
					Symbol:      sym.Name,
					Tags:        []string{sym.KindName},
				})
			}
		}

		// 同时索引整个文件（全局语义搜索兜底，hash 未变则跳过）
		embedTextAll := string(f.content)
		if same, _ := CheckContentHash(cwd, f.relPath, "", embedTextAll); !same {
			_ = AddToQueue(cwd, EmbedTask{
				EmbedText:   embedTextAll,
				FullContent: string(f.content),
				FilePath:    f.relPath,
				Symbol:      "",
				Tags:        []string{"file"},
			})
		}
	}

	// 删除已被移除的文件对应的索引条目
	for p := range existingPathSet {
		if !scannedPaths[p] {
			_ = RemoveFile(cwd, p)
		}
	}

	// 检查队列剩余，广播 embedding 进度
	if qLen := DirectoryStatus(cwd).QueueLen; qLen > 0 {
		initQueueLen := qLen
		broadcastFn(IndexStatus{
			Total:     initQueueLen,
			Processed: 0,
			Remaining: qLen,
			Status:    "embedding",
		})
		// 后台轮询队列进度直到完成
		go func() {
			for {
				time.Sleep(500 * time.Millisecond)
				ds := DirectoryStatus(cwd)
				if ds.QueueLen == 0 {
					broadcastFn(IndexStatus{
						Total:     initQueueLen,
						Processed: initQueueLen,
						Status:    "completed",
					})
					return
				}
				broadcastFn(IndexStatus{
					Total:     initQueueLen,
					Processed: initQueueLen - ds.QueueLen,
					Remaining: ds.QueueLen,
					Status:    "embedding",
				})
			}
		}()
	} else {
		broadcastFn(IndexStatus{
			Total:     total,
			Processed: total,
			Status:    "completed",
		})
	}
	return nil
}
