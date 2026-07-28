package search

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	u "github.com/cxykevin/alkaid0/utils"
)

const toolName = "search"

//go:embed prompt.md
var prompt string

var logger = log.New("tools:search")

var paras = map[string]parser.ToolParameters{
	"query": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The search query. Must Be First Parameter",
	},
	"online": {
		Type:        parser.ToolTypeBoolean,
		Required:    true,
		Description: "Whether to search online. Currently only false is supported. Must Be Second Parameter",
	},
	"include_gitignored": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "Whether to also search files matching .gitignore patterns. Default is false",
	},
	"max_results": {
		Type:        parser.ToolTypeNumber,
		Required:    false,
		Description: "Maximum results per search source. Default is 10",
	},
	"context_search_type": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: "Context engine search mode: \"auto\" (BM25+vector hybrid), \"bm25\" (keyword only), \"vector\" (semantic only). Default is \"auto\"",
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
	".alkaid0_skip": true,
	".git":          true,
	".alkaid0":      true,
	".alkaid":       true,
	".cursor":       true,
	".github":       true,
	"CLAUDE.md":     true,
	"GEMINI.md":     true,
	"AGENTS.md":     true,
	"IFLOW.md":      true,
	".env":          true,
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
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".bmp":  true,
	".ico":  true,
	".svg":  true,
	".webp": true,
	".woff": true,
	".woff2": true,
	".ttf":  true,
	".eot":  true,
	".otf":  true,
	".pdf":  true,
	".zip":  true,
	".tar":  true,
	".gz":   true,
	".bz2":  true,
	".xz":   true,
	".zst":  true,
	".7z":   true,
	".rar":  true,
	".exe":  true,
	".dll":  true,
	".so":   true,
	".dylib": true,
	".bin":  true,
	".o":    true,
	".a":    true,
	".lib":  true,
	".obj":  true,
	".pyc":  true,
	".pyo":  true,
	".class": true,
	".jar":  true,
	".war":  true,
	".deb":  true,
	".rpm":  true,
	".AppImage": true,
	".dmg":  true,
	".iso":  true,
	".img":  true,
	".lock": true,
	".db":   true,
	".sqlite": true,
	".sqlite3": true,
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

func matchDoubleStar(pattern, path string) bool {
	patParts := strings.Split(pattern, "**")
	if len(patParts) == 1 {
		ok, _ := filepath.Match(pattern, path)
		return ok
	}

	prefix := patParts[0]
	suffix := patParts[len(patParts)-1]

	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return false
	}
	if suffix != "" && !strings.HasSuffix(path, suffix) {
		return false
	}
	if prefix != "" || suffix != "" {
		middle := path[len(prefix) : len(path)-len(suffix)]
		if !strings.Contains(middle, "/") {
			return false
		}
	}
	return true
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
	session.ToolCallingContext[toolCallID] = respObj
	session.ToolCallingType[toolCallID] = toolName
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
		return errResult("online search is not yet supported", cross)
	}

	includeGitignored, _ := getBoolParamDefault(mp, "include_gitignored", false)
	maxResults, _ := getIntParamDefault(mp, "max_results", 10)
	contextSearchType, _ := getStringParamDefault(mp, "context_search_type", "auto")

	root := session.Root
	if root == "" {
		root = "."
	}

	ctx := session.GetContext()
	if ctx == nil {
		ctx = context.Background()
	}

	// 阶段一：AI Grep
	grepResults := aiGrep(ctx, root, query, includeGitignored, maxResults)

	// 阶段二：Context Engine 搜索
	ctxResults := contextSearch(ctx, root, query, contextSearchType, maxResults)

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

func aiGrep(ctx context.Context, root, query string, includeGitignored bool, maxResults int) []grepResult {
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
		fileResults := grepFile(path, relPath, query, maxResults-len(results))
		results = append(results, fileResults...)

		return nil
	})

	if results == nil {
		results = []grepResult{}
	}
	return results
}

func grepFile(path, relPath, query string, maxResults int) []grepResult {
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
		lineNum++
		line := scanner.Text()
		if strings.Contains(line, query) {
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
// Context Engine 搜索（通过函数指针解耦，由 ui/startup 注册）
// ---------------------------------------------------------------------------

// ContextSearchResult 上下文搜索结果条目
type ContextSearchResult struct {
	FilePath string
	Symbol   string
	Content  string
	Score    float64
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

	out := make([]searchResult, 0, len(results))
	for _, r := range results {
		content := r.Symbol
		if content == "" {
			content = r.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
		}
		out = append(out, searchResult{
			Source:   "context",
			FilePath: r.FilePath,
			Content:  content,
			Symbol:   r.Symbol,
			Score:    r.Score,
		})
	}
	return out
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
// 工具注册
// ---------------------------------------------------------------------------

func load() string {
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

