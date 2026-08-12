package search

import (
	"bufio"
	"context"
	_ "embed" // embed
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	u "github.com/cxykevin/alkaid0/utils"

	searchengine "github.com/cxykevin/alkaid0-search-engine/search"
	"github.com/cxykevin/alkaid0/config"
)

const toolName = "search"

//go:embed prompt.md
var prompt string

// SummaryPrompt 搜索摘要提示词
//
//go:embed search_summary.md
var SummaryPrompt string

var logger = log.New("tools:search")

const pathDesp = "Search path (directory) when online=false Defaults to workspace root if empty. It will be used as set providers when online=true(Comma-separated list of search providers to use for online search (e.g., 'bing,tavily,github')). If not specified, all providers will be used. Available providers: "

var paras = map[string]parser.ToolParameters{
	"query": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The search query. Must Be First Parameter",
	},
	"online": {
		Type:        parser.ToolTypeBoolean,
		Required:    true,
		Description: "Whether to search online. If true, searches the internet via configured search engines (Bing/GitHub/arXiv/Tavily/...) and summarizes results via LLM. Must Be Second Parameter",
	},
	"path": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: "<will_be_replaced_desp>",
	},
	"recursive": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "Whether to search recursively into subdirectories. Default is true. Only when online=false",
	},
	"include_ignored": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "Whether to also search files matching .gitignore patterns. Default is false. Only when online=false",
	},
	"max_results": {
		Type:        parser.ToolTypeNumber,
		Required:    false,
		Description: "Maximum results per search source. Default is 10",
	},
}

// grepResult 单条 grep 匹配结果
type grepResult struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Content  string `json:"content"`
}

// searchResult 统一搜索结果
type searchResult struct {
	Source   string  `json:"source"` // "grep" 或 "context"
	FilePath string  `json:"file_path"`
	Line     int     `json:"line,omitempty"`
	Content  string  `json:"content,omitempty"`
	Symbol   string  `json:"symbol,omitempty"`
	Score    float64 `json:"score,omitempty"`
}

// ---------------------------------------------------------------------------
// 目录/文件名黑名单（与 tools/tools/tree/files.go 保持一致）
// ---------------------------------------------------------------------------

var dirBlacklists = map[string]bool{
	".alkaid0_skip":    true,
	".git":             true,
	".alkaid0":         true,
	".alkaid":          true,
	".cursor":          true,
	".github":          true,
	"CLAUDE.md":        true,
	"GEMINI.md":        true,
	"AGENTS.md":        true,
	"IFLOW.md":         true,
	".env":             true,
	".env.example":     true,
	".env.local":       true,
	".env.production":  true,
	".env.development": true,
	".ssh":             true,
	"id_rsa":           true,
	"id_rsa.pub":       true,
	"id_dsa":           true,
	"id_ecdsa":         true,
	"id_ed25519":       true,
	"id_ed25519.pub":   true,
	"authorized_keys":  true,
	"known_hosts":      true,
	"config":           true,
	".aws":             true,
	".gcp":             true,
	".azure":           true,
	".kube":            true,
	".docker":          true,
	"credentials":      true,
	"secret":           true,
	"secrets":          true,
	"token":            true,
	"tokens":           true,
	".npmrc":           true,
	".netrc":           true,
	".pgpass":          true,
	"DS_Store":         true,
	".DS_Store":        true,
	".Trash":           true,
	"Thumbs.db":        true,
	"desktop.ini":      true,
}

// binaryExts 常见二进制文件扩展名，跳过
var binaryExts = map[string]bool{
	".png":      true,
	".jpg":      true,
	".jpeg":     true,
	".gif":      true,
	".bmp":      true,
	".ico":      true,
	".svg":      true,
	".webp":     true,
	".woff":     true,
	".woff2":    true,
	".ttf":      true,
	".eot":      true,
	".otf":      true,
	".pdf":      true,
	".zip":      true,
	".tar":      true,
	".gz":       true,
	".bz2":      true,
	".xz":       true,
	".zst":      true,
	".7z":       true,
	".rar":      true,
	".exe":      true,
	".dll":      true,
	".so":       true,
	".dylib":    true,
	".bin":      true,
	".o":        true,
	".a":        true,
	".lib":      true,
	".obj":      true,
	".pyc":      true,
	".pyo":      true,
	".class":    true,
	".jar":      true,
	".war":      true,
	".deb":      true,
	".rpm":      true,
	".AppImage": true,
	".dmg":      true,
	".iso":      true,
	".img":      true,
	".lock":     true,
	".db":       true,
	".sqlite":   true,
	".sqlite3":  true,
}

// ---------------------------------------------------------------------------
// gitignore 解析与匹配（独立实现，不依赖 context/codebase 包）
// ---------------------------------------------------------------------------

type gitignorePattern struct {
	pattern  string
	negate   bool
	dirOnly  bool
	rooted   bool
	hasSlash bool
}

func loadGitignore(dir string) []gitignorePattern {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	return parseGitignore(string(data))
}

func parseGitignore(content string) []gitignorePattern {
	lines := strings.Split(content, "\n")
	patterns := make([]gitignorePattern, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
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

func normalizePath(path string) string {
	path = filepath.ToSlash(path)
	path = strings.TrimSuffix(path, "/")
	return path
}

func matchPattern(p gitignorePattern, path string) bool {
	pattern := p.pattern

	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, path)
	}

	if !p.hasSlash && !p.rooted {
		_, name := filepath.Split(path)
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
		for part := range strings.SplitSeq(path, "/") {
			if ok, _ := filepath.Match(pattern, part); ok {
				return true
			}
		}
		return false
	}

	if p.rooted {
		if ok, _ := filepath.Match(pattern, path); ok {
			return true
		}
		return false
	}

	// hasSlash: 从路径末尾逐段检查
	parts := strings.Split(path, "/")
	for i := range parts {
		candidate := strings.Join(parts[i:], "/")
		if ok, _ := filepath.Match(pattern, candidate); ok {
			return true
		}
	}
	return false
}

// matchDoubleStar 实现 gitignore 的 ** 语义：** 匹配任意数量的路径段（含零个）。
// 例如 "**/foo" 匹配任意深度（含根目录）的 foo；"a/**/b" 匹配 a 与 b 之间的任意层。
func matchDoubleStar(pattern, path string) bool {
	patParts := strings.Split(pattern, "**")
	if len(patParts) == 1 {
		ok, _ := filepath.Match(pattern, path)
		return ok
	}

	// 路径按 / 分段（忽略前导 /）
	path = strings.TrimPrefix(path, "/")
	pathParts := strings.Split(path, "/")

	// 固定段列表（** 两侧的非空段按 / 拆分）
	segments := make([]string, 0, len(patParts))
	for _, p := range patParts {
		p = strings.Trim(p, "/")
		if p != "" {
			segments = append(segments, strings.Split(p, "/")...)
		}
	}
	if len(segments) == 0 {
		return true // 纯 ** 模式
	}

	// 按顺序在路径段中匹配固定段；** 允许跳过任意数量的路径段
	var match func(segIdx, pathIdx int) bool
	match = func(segIdx, pathIdx int) bool {
		if segIdx == len(segments) {
			return true
		}
		for i := pathIdx; i < len(pathParts); i++ {
			if ok, _ := filepath.Match(segments[segIdx], pathParts[i]); ok {
				if match(segIdx+1, i+1) {
					return true
				}
			}
		}
		return false
	}
	return match(0, 0)
}

// ---------------------------------------------------------------------------
// updateInfo — OnHook: 搜索参数预览
// ---------------------------------------------------------------------------

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	respString := ""
	if queryPtr, ok := mp["query"]; ok && queryPtr != nil {
		if query, ok := (*queryPtr).(string); ok {
			respString += "Query: " + query + "\n"
		}
	}
	if onlinePtr, ok := mp["online"]; ok && onlinePtr != nil {
		if online, ok := (*onlinePtr).(bool); ok {
			respString += "Online: " + u.Ternary(online, "true", "false") + "\n"
		}
	}
	if pathPtr, ok := mp["path"]; ok && pathPtr != nil {
		if path, ok := (*pathPtr).(string); ok && path != "" {
			respString += "Path: " + path + "\n"
		}
	}
	if recursivePtr, ok := mp["recursive"]; ok && recursivePtr != nil {
		if recursive, ok := (*recursivePtr).(bool); ok {
			respString += "Recursive: " + u.Ternary(recursive, "true", "false") + "\n"
		}
	}
	respObj := []u.H{{
		"type": "content",
		"content": u.H{
			"type": "text",
			"text": respString,
		},
	}, {
		"type":      "alk.cxykevin.top/calling_info",
		"name":      toolName,
		"messageID": session.CurrentMessageID,
		"args": u.H{
			"query":  mp["query"],
			"online": mp["online"],
		},
	}}
	session.SetToolCalling(toolCallID, respObj, toolName)
	return true, cross, nil
}

// ---------------------------------------------------------------------------
// runSearch — PostHook: 执行搜索
// ---------------------------------------------------------------------------

func runSearch(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	// 提取参数
	query, err := getStringParam(mp, "query")
	if err != nil {
		return errResult(err.Error(), cross)
	}

	online, err := getBoolParam(mp, "online")
	if err != nil {
		return errResult(err.Error(), cross)
	}
	if online {
		return runOnlineSearch(session, mp, cross)
	}

	includeGitignored, _ := getBoolParamDefault(mp, "include_ignored", false)
	maxResults, _ := getIntParamDefault(mp, "max_results", 10)
	recursive, _ := getBoolParamDefault(mp, "recursive", true)

	// 确定搜索根路径
	root := session.Root
	if root == "" {
		root = "."
	}
	if searchPath, _ := getStringParamDefault(mp, "path", ""); searchPath != "" {
		// 校验 path 不能逃逸工作区：拒绝绝对路径与 '..' 穿越
		if filepath.IsAbs(searchPath) {
			return errResult("path must be relative to the workspace", cross)
		}
		cleanedSearch := filepath.Clean(searchPath)
		if cleanedSearch == ".." || strings.HasPrefix(cleanedSearch, ".."+string(filepath.Separator)) {
			return errResult("path escapes the workspace", cross)
		}
		root = filepath.Join(root, cleanedSearch)
	}

	ctx := session.GetContext()
	if ctx == nil {
		ctx = context.Background()
	}

	// 编译正则（支持 /pattern/flags 格式或通配符表达式）
	var re *regexp.Regexp
	grepQuery := query
	ctxQuery := query
	grepMax := maxResults
	ctxMax := maxResults

	// 检查是否为 /pattern/flags 格式的原始正则
	if pattern, flags, ok := parseDelimitedRegex(query); ok {
		// 原始正则，直接编译
		reExpr := buildRegex(pattern, flags)
		var err error
		re, err = regexp.Compile(reExpr)
		if err == nil {
			grepQuery = pattern
			ctxQuery = extractKeywords(pattern)
			// 80% 给 grep，20% 给 context
			grepMax = max(int(float64(maxResults)*0.8), 1)
			ctxMax = max(maxResults-grepMax, 1)
		}
	} else if isGrepExpr(query) {
		var err error
		re, err = wildcardToRegex(query)
		if err == nil {
			// 提取纯关键词给 Context Engine
			ctxQuery = extractKeywords(query)
			// 80% 给 grep，20% 给 context
			grepMax = max(int(float64(maxResults)*0.8), 1)
			ctxMax = max(maxResults-grepMax, 1)
		} else {
			// 编译失败就当普通查询
		}
	}

	// 阶段一：AI Grep
	grepResults := aiGrep(ctx, root, grepQuery, includeGitignored, recursive, re, grepMax)

	// 阶段二：Context Engine 搜索
	ctxResults := contextSearch(ctx, root, ctxQuery, "auto", ctxMax)

	// 合并结果
	results := make([]searchResult, 0)
	for _, r := range grepResults {
		results = append(results, searchResult{
			Source:   "grep",
			FilePath: r.FilePath,
			Line:     r.Line,
			Content:  r.Content,
		})
	}
	for _, r := range ctxResults {
		results = append(results, r)
	}

	// 格式化为可读字符串
	output := formatResults(results)
	outAny := any(output)
	successAny := any(true)

	return false, cross, map[string]*any{
		"success": &successAny,
		"output":  &outAny,
	}, nil
}

// ---------------------------------------------------------------------------
// AI Grep
// ---------------------------------------------------------------------------

func aiGrep(ctx context.Context, root, query string, includeGitignored bool, recursive bool, re *regexp.Regexp, maxResults int) []grepResult {
	giPatterns := loadGitignore(root)
	var results []grepResult

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的路径
		}

		select {
		case <-ctx.Done():
			return filepath.SkipAll
		default:
		}

		if len(results) >= maxResults {
			return filepath.SkipAll
		}

		// 跳过根目录
		if path == root {
			return nil
		}

		relPath, _ := filepath.Rel(root, path)
		name := d.Name()

		// 跳过黑名单
		if dirBlacklists[name] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// 跳过 .gitignore 匹配
		if !includeGitignored && len(giPatterns) > 0 {
			if matchGitignore(giPatterns, relPath, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// 跳过目录（黑名单已过滤，剩下的目录需要递归进去）
		if d.IsDir() {
			if !recursive {
				return filepath.SkipDir
			}
			return nil
		}

		// 跳过二进制文件
		ext := strings.ToLower(filepath.Ext(name))
		if binaryExts[ext] {
			return nil
		}

		// 大文件跳过（> 10MB）
		info, err := d.Info()
		if err == nil && info.Size() > 10*1024*1024 {
			return nil
		}

		// 读取并搜索文件内容
		fileResults := grepFile(path, relPath, query, re, maxResults-len(results))
		results = append(results, fileResults...)

		return nil
	})

	if results == nil {
		results = []grepResult{}
	}
	return results
}

func grepFile(path, relPath, query string, re *regexp.Regexp, maxResults int) []grepResult {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var results []grepResult
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
	lineNum := 0
	for scanner.Scan() {
		if scanner.Err() != nil {
			break
		}
		lineNum++
		line := scanner.Text()
		match := false
		if re != nil {
			match = re.MatchString(line)
		} else {
			match = strings.Contains(line, query)
		}
		if match {
			results = append(results, grepResult{
				FilePath: relPath,
				Line:     lineNum,
				Content:  strings.TrimSpace(line),
			})
			if len(results) >= maxResults {
				break
			}
		}
	}
	return results
}

// ---------------------------------------------------------------------------
// grep 表达式检测与关键词提取
// ---------------------------------------------------------------------------

// isGrepExpr 判断查询字符串是否包含显式通配符（*, ?, [...]）
func isGrepExpr(query string) bool {
	// 含有 *
	if strings.Contains(query, "*") {
		return true
	}
	// 含有 ?
	if strings.Contains(query, "?") {
		return true
	}
	// 含有字符类 [...]
	if strings.Contains(query, "[") && strings.Contains(query, "]") {
		return true
	}
	return false
}

// wildcardToRegex 将通配符模式（*, ?）转换为正则表达式
func wildcardToRegex(pattern string) (*regexp.Regexp, error) {
	var buf strings.Builder
	buf.WriteString("(?s)")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch {
		case ch == '*':
			buf.WriteString(".*")
		case ch == '?':
			buf.WriteString(".")
		case ch == '[':
			// 字符类 [...] 原样保留
			buf.WriteByte(ch)
			i++
			for i < len(pattern) && pattern[i] != ']' {
				buf.WriteByte(pattern[i])
				i++
			}
			if i < len(pattern) {
				buf.WriteByte(pattern[i])
			}
		case ch == '.' || ch == '+' || ch == '^' || ch == '$' ||
			ch == '|' || ch == '(' || ch == ')' || ch == '{' || ch == '}' || ch == '\\':
			buf.WriteByte('\\')
			buf.WriteByte(ch)
		default:
			buf.WriteByte(ch)
		}
	}
	return regexp.Compile(buf.String())
}

// extractKeywords 从通配符/正则表达式中提取纯关键词用于 Context Engine 搜索
func extractKeywords(expr string) string {
	// 去掉字符类
	re := regexp.MustCompile(`\[[^\]]*\]`)
	result := re.ReplaceAllString(expr, " ")
	// 去掉通配符 * 和 ?
	result = strings.ReplaceAll(result, "*", " ")
	result = strings.ReplaceAll(result, "?", " ")
	// 去掉量词和特殊字符
	re = regexp.MustCompile(`[+^$|.()\\{}\[\]]+`)
	result = re.ReplaceAllString(result, " ")
	// 去掉多余的空白
	re = regexp.MustCompile(`\s+`)
	result = strings.TrimSpace(re.ReplaceAllString(result, " "))
	return result
}

// parseDelimitedRegex 解析 /pattern/flags 格式的正则表达式
func parseDelimitedRegex(query string) (pattern, flags string, ok bool) {
	if len(query) < 3 || query[0] != '/' {
		return "", "", false
	}
	// 找到结尾的 /
	lastSlash := strings.LastIndex(query, "/")
	if lastSlash <= 0 {
		return "", "", false
	}
	pattern = query[1:lastSlash]
	if pattern == "" {
		return "", "", false
	}
	flags = query[lastSlash+1:]
	// 验证 flags：只允许字母
	for _, f := range flags {
		if !((f >= 'a' && f <= 'z') || (f >= 'A' && f <= 'Z')) {
			return "", "", false
		}
	}
	return pattern, flags, true
}

// buildRegex 根据 pattern 和 flags 构建 Go 正则表达式字符串
func buildRegex(pattern, flags string) string {
	var prefix strings.Builder
	for _, f := range flags {
		switch f {
		case 'i':
			prefix.WriteString("(?i)")
		case 'm':
			prefix.WriteString("(?m)")
		case 's':
			prefix.WriteString("(?s)")
		}
		// 'g' 在 Go 中无意义（MatchString 默认全局）
	}
	return prefix.String() + pattern
}

// ---------------------------------------------------------------------------
// Context Engine 搜索（通过函数指针解耦，由 ui/startup 注册）
// ---------------------------------------------------------------------------

// ContextSearchResult 上下文搜索结果条目
type ContextSearchResult struct {
	FilePath string
	Symbol   string
	Content  string
	Score    float64
	Tags     string
}

// ContextSearchFn 上下文搜索函数类型
type ContextSearchFn func(ctx context.Context, directory string, searchType int, query string, limit int) ([]ContextSearchResult, error)

// contextSearchFn 函数指针，由 SetContextSearchFn 在启动时注入
var contextSearchFn ContextSearchFn

// SetContextSearchFn 设置上下文搜索函数（在 ui/startup 中调用，避免循环导入）
func SetContextSearchFn(fn ContextSearchFn) {
	contextSearchFn = fn
}

const (
	searchTypeAuto   = 0
	searchTypeBM25   = 1
	searchTypeVector = 2
)

func contextSearch(ctx context.Context, root, query, searchTypeStr string, maxResults int) []searchResult {
	if contextSearchFn == nil {
		logger.Debug("context search not available (not initialized)")
		return nil
	}

	var st int
	switch searchTypeStr {
	case "bm25":
		st = searchTypeBM25
	case "vector":
		st = searchTypeVector
	default:
		st = searchTypeAuto
	}

	results, err := contextSearchFn(ctx, root, st, query, maxResults)
	if err != nil {
		logger.Warn("context search error: %v", err)
		return nil
	}

	var normal, tagged []searchResult
	for _, r := range results {
		content := r.Symbol
		if content == "" {
			content = r.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
		}
		sr := searchResult{
			Source:   "context",
			FilePath: r.FilePath,
			Content:  content,
			Symbol:   r.Symbol,
			Score:    r.Score,
		}
		// tempfs/chathistory 排到底部，限制总数不超过 3 条
		if strings.Contains(r.Tags, "tempfs") || strings.Contains(r.Tags, "chathistory") {
			tagged = append(tagged, sr)
		} else {
			normal = append(normal, sr)
		}
	}
	// 限制 tagged 结果最多 3 条
	if len(tagged) > 3 {
		tagged = tagged[:3]
	}
	return append(normal, tagged...)
}

// ---------------------------------------------------------------------------
// 格式化输出
// ---------------------------------------------------------------------------

func formatResults(results []searchResult) string {
	if len(results) == 0 {
		return "No results found."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d result(s):\n\n", len(results))

	grepCount := 0
	ctxCount := 0
	for _, r := range results {
		if r.Source == "grep" {
			grepCount++
			fmt.Fprintf(&b, "[grep] %s:%d\n  %s\n", r.FilePath, r.Line, r.Content)
		} else {
			ctxCount++
			scoreStr := fmt.Sprintf(" (score: %.4f)", r.Score)
			if r.Symbol != "" {
				fmt.Fprintf(&b, "[context] %s [%s]%s\n  %s\n", r.FilePath, r.Symbol, scoreStr, r.Content)
			} else {
				fmt.Fprintf(&b, "[context] %s%s\n  %s\n", r.FilePath, scoreStr, r.Content)
			}
		}
		b.WriteString("\n")
	}

	if grepCount > 0 || ctxCount > 0 {
		fmt.Fprintf(&b, "---\n%d grep match(es), %d context result(s)\n", grepCount, ctxCount)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// 参数提取工具
// ---------------------------------------------------------------------------

func getStringParam(mp map[string]*any, key string) (string, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	v, ok := (*p).(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string", key)
	}
	if v == "" {
		return "", fmt.Errorf("parameter %s cannot be empty", key)
	}
	return v, nil
}

func getStringParamDefault(mp map[string]*any, key, def string) (string, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return def, nil
	}
	v, ok := (*p).(string)
	if !ok {
		return def, nil
	}
	return v, nil
}

func getBoolParam(mp map[string]*any, key string) (bool, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return false, fmt.Errorf("missing required parameter: %s", key)
	}
	v, ok := (*p).(bool)
	if !ok {
		return false, fmt.Errorf("parameter %s must be a boolean", key)
	}
	return v, nil
}

func getBoolParamDefault(mp map[string]*any, key string, def bool) (bool, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return def, nil
	}
	v, ok := (*p).(bool)
	if !ok {
		return def, nil
	}
	return v, nil
}

func getIntParamDefault(mp map[string]*any, key string, def int) (int, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return def, nil
	}
	switch v := (*p).(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return def, nil
	}
}

func errResult(msg string, cross []*any) (bool, []*any, map[string]*any, error) {
	f := false
	s := any(f)
	e := any(msg)
	return false, cross, map[string]*any{"success": &s, "error": &e}, nil
}

// ---------------------------------------------------------------------------
// Online Search — runOnlineSearch (online=true)
// ---------------------------------------------------------------------------

// SummarizeFn 搜索结果总结函数类型，由 SetSummarizeFn 注入以避免循环导入
type SummarizeFn func(ctx context.Context, rawResult, query string, modelID int32) (string, error)

// summarizeFn 函数指针，由 SetSummarizeFn 在启动时注入
var summarizeFn SummarizeFn

// SetSummarizeFn 设置搜索结果总结函数（在 ui/startup 中调用，避免循环导入）
func SetSummarizeFn(fn SummarizeFn) {
	summarizeFn = fn
}

// runOnlineSearch 执行在线搜索，并通过 LLM 总结结果
func runOnlineSearch(_ *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	query, err := getStringParam(mp, "query")
	if err != nil {
		return errResult(err.Error(), cross)
	}

	// 读取在线搜索配置
	onCfg := config.GlobalConfig.Context.OnlineSearch

	// 构建搜索引擎配置
	seCfg := onCfg

	// 执行搜索
	searchCtx, cancel := context.WithTimeout(context.Background(), time.Duration(onCfg.Timeout)*time.Second)
	defer cancel()

	// 提取 path 参数并构建搜索选项
	var searchOpts []searchengine.Option
	if providersStr, _ := getStringParamDefault(mp, "path", ""); providersStr != "" {
		logger.Info("using specified providers: %s", providersStr)
		searchOpts = append(searchOpts, searchengine.WithProviders(providersStr))
	}

	rawResult, err := searchengine.Search(searchCtx, query, &seCfg, searchOpts...)
	if err != nil {
		logger.Error("online search failed: %v", err)
		return errResult(fmt.Sprintf("online search failed: %v", err), cross)
	}

	// 获取总结模型
	summaryModelID := config.GlobalConfig.Context.SearchSummaryModel
	if summaryModelID == 0 {
		summaryModelID = config.GlobalConfig.Agent.SummaryModel
	}
	if summaryModelID == 0 {
		summaryModelID = config.GlobalConfig.Model.DefaultModelID
	}
	found := summaryModelID != 0
	if !found {
		// 兜底：取 Models 中第一个可用模型
		for id := range config.GlobalConfig.Model.Models {
			summaryModelID = id
			found = true
			break
		}
	}
	if found {
		logger.Info("search summary using modelID=%d", summaryModelID)
	} else {
		logger.Warn("no model configured for search summary, returning raw results")
	}

	// 通过函数指针调用 LLM 总结
	if summarizeFn == nil {
		logger.Error("summarize function not set (call SetSummarizeFn in startup)")
		// 降级返回原始搜索结果
		outAny := any(rawResult)
		successAny := any(true)
		return false, cross, map[string]*any{
			"success": &successAny,
			"output":  &outAny,
		}, nil
	}

	summary, err := summarizeFn(context.Background(), rawResult, query, summaryModelID)
	if err != nil {
		logger.Error("failed to summarize search result: %v", err)
		// 总结失败时降级返回原始搜索结果
		logger.Info("falling back to raw search results for query: %s", query)
		outAny := any(rawResult)
		successAny := any(true)
		return false, cross, map[string]*any{
			"success": &successAny,
			"output":  &outAny,
		}, nil
	}

	logger.Info("search summary completed for query: %s (len=%d)", query, len(summary))
	outAny := any(summary)
	successAny := any(true)
	return false, cross, map[string]*any{
		"success": &successAny,
		"output":  &outAny,
	}, nil
}

// ---------------------------------------------------------------------------
// 工具注册
// ---------------------------------------------------------------------------

// UpdateToolPathDesp 更新工具描述
func UpdateToolPathDesp() {
	onCfg := config.GlobalConfig.Context.OnlineSearch
	seCfg := onCfg
	v := paras["path"]
	v.Description = pathDesp + strings.Join(searchengine.EnabledProviders(&seCfg), ",")
	// map 取值是值拷贝，必须写回，否则描述始终停留在占位符 <will_be_replaced_desp>
	paras["path"] = v
}

func load() string {
	config.AddReloadHook(UpdateToolPathDesp)
	UpdateToolPathDesp()
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            toolName,
		UserDescription: prompt,
		Parameters:      paras,
		ID:              toolName,
	})
	if err := actions.HookTool(toolName, &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     runSearch,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
