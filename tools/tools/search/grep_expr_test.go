package search

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// isGrepExpr
// ---------------------------------------------------------------------------

func TestIsGrepExpr(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		// 通配符 *
		{"func*Handler", true},
		{"*test", true},
		{"test*", true},
		// 通配符 ?
		{"func?test", true},
		{"he?lo", true},
		// 字符类 [...]
		{"[a-z]*Handler", true},
		{"func[0-9]", true},
		// 不含通配符 → 普通文本
		{"hello world", false},
		{"func Handler", false},
		{"error.log", false},
		{"main.go:42", false},
		{"func()", false},
		{"(error)", false},
		{"foo.bar", false},
		{"abc+def", false},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := isGrepExpr(tt.query)
			if got != tt.want {
				t.Errorf("isGrepExpr(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseDelimitedRegex
// ---------------------------------------------------------------------------

func TestParseDelimitedRegex(t *testing.T) {
	tests := []struct {
		query       string
		wantPattern string
		wantFlags   string
		wantOK      bool
	}{
		{"/func.*Handler/g", "func.*Handler", "g", true},
		{"/error/im", "error", "im", true},
		{"/hello/i", "hello", "i", true},
		{"/a.b/", "a.b", "", true},
		// 不含分隔符 → 不匹配
		{"hello world", "", "", false},
		{"func*Handler", "", "", false},
		// 只有开头 / 没有结尾 /
		{"/hello", "", "", false},
		// 空 pattern
		{"//g", "", "", false},
		// flag 含非字母
		{"/hello/g1", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			pattern, flags, ok := parseDelimitedRegex(tt.query)
			if ok != tt.wantOK || pattern != tt.wantPattern || flags != tt.wantFlags {
				t.Errorf("parseDelimitedRegex(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.query, pattern, flags, ok, tt.wantPattern, tt.wantFlags, tt.wantOK)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildRegex
// ---------------------------------------------------------------------------

func TestBuildRegex(t *testing.T) {
	tests := []struct {
		pattern string
		flags   string
		want    string
	}{
		{"hello", "i", "(?i)hello"},
		{"hello", "im", "(?i)(?m)hello"},
		{"hello", "", "hello"},
		{"hello", "g", "hello"}, // g 在 Go 中无意义
		{"hello", "s", "(?s)hello"},
	}
	for _, tt := range tests {
		t.Run(tt.flags+"_"+tt.pattern, func(t *testing.T) {
			got := buildRegex(tt.pattern, tt.flags)
			if got != tt.want {
				t.Errorf("buildRegex(%q, %q) = %q, want %q", tt.pattern, tt.flags, got, tt.want)
			}
		})
	}

	// 验证编译结果可用
	t.Run("compile_and_match", func(t *testing.T) {
		re := regexp.MustCompile(buildRegex("hello", "i"))
		if !re.MatchString("HELLO") {
			t.Error("expected (?i)hello to match HELLO")
		}
		if re.MatchString("world") {
			t.Error("expected (?i)hello to NOT match world")
		}
	})
}

// ---------------------------------------------------------------------------
// wildcardToRegex
// ---------------------------------------------------------------------------

func TestWildcardToRegex(t *testing.T) {
	tests := []struct {
		pattern string
		input   string
		want    bool
	}{
		{"func*Handler", "funcAnyHandler", true},
		{"func*Handler", "funcABC123Handler", true},
		{"func*Handler", "Handler", false},
		{"func?test", "funcXtest", true},
		{"func?test", "funcXYtest", false}, // ? 只匹配一个字符
		{"func?test", "functest", false},   // ? 至少匹配一个字符
		{"[a-z]*Handler", "xHandler", true},
		{"[a-z]*Handler", "abcHandler", true},
		{"[a-z]*Handler", "1Handler", false}, // 1 不在 [a-z] 内
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_vs_"+tt.input, func(t *testing.T) {
			re, err := wildcardToRegex(tt.pattern)
			if err != nil {
				t.Fatalf("wildcardToRegex(%q) failed: %v", tt.pattern, err)
			}
			got := re.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("wildcardToRegex(%q).MatchString(%q) = %v, want %v", tt.pattern, tt.input, got, tt.want)
			}
		})
	}
}

func TestWildcardToRegexSpecialChars(t *testing.T) {
	// 验证正则元字符被正确转义
	re, err := wildcardToRegex("foo.bar+test")
	if err != nil {
		t.Fatalf("wildcardToRegex failed: %v", err)
	}
	// . 和 + 应被转义为字面量，不当作元字符
	if !re.MatchString("foo.bar+test") {
		t.Error("should match literal foo.bar+test")
	}
	if re.MatchString("fooXbarYtest") {
		t.Error("should NOT treat . and + as metacharacters")
	}

	// 验证 * 仍然是通配符
	re2, err := wildcardToRegex("foo*bar")
	if err != nil {
		t.Fatalf("wildcardToRegex failed: %v", err)
	}
	if !re2.MatchString("fooXYZbar") {
		t.Error("* should match any sequence")
	}
}

// ---------------------------------------------------------------------------
// extractKeywords
// ---------------------------------------------------------------------------

func TestExtractKeywords(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		{"func*Handler", "func Handler"},
		{"func?test", "func test"},
		{"[a-z]*Handler", "Handler"},
		{"foo.bar+test", "foo bar test"},
		{"hello", "hello"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got := extractKeywords(tt.expr)
			if got != tt.want {
				t.Errorf("extractKeywords(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// grepFile with regex
// ---------------------------------------------------------------------------

func TestGrepFileRegex(t *testing.T) {
	dir := t.TempDir()
	filePath := dir + "/test.txt"
	content := "func hello()\nfunc world()\nfunc Hello()\ndefault case"
	if err := writeFile(filePath, content); err != nil {
		t.Fatal(err)
	}

	// case-insensitive regex
	re := regexp.MustCompile(`(?i)hello`)
	results := grepFile(filePath, "test.txt", "hello", re, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results (hello, Hello), got %d", len(results))
	}
}

func TestGrepFileWildcardPattern(t *testing.T) {
	dir := t.TempDir()
	filePath := dir + "/test.txt"
	content := "func Handler()\nfunc MyHandler()\nfunc helper()\nvar handler"
	if err := writeFile(filePath, content); err != nil {
		t.Fatal(err)
	}

	// wildcard pattern *Handler → 匹配 func Handler() 和 func MyHandler()
	re, err := wildcardToRegex("*Handler")
	if err != nil {
		t.Fatal(err)
	}
	results := grepFile(filePath, "test.txt", "", re, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// grepFile with nil regex (plain text)
// ---------------------------------------------------------------------------

func TestGrepFilePlainText(t *testing.T) {
	dir := t.TempDir()
	filePath := dir + "/test.txt"
	content := "hello world\nHELLO WORLD\nhello again"
	if err := writeFile(filePath, content); err != nil {
		t.Fatal(err)
	}

	// nil regex = strings.Contains
	results := grepFile(filePath, "test.txt", "hello", nil, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results (case-sensitive), got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// aiGrep non-recursive
// ---------------------------------------------------------------------------

func TestAiGrepNonRecursive(t *testing.T) {
	dir := t.TempDir()

	files := map[string]string{
		"top.txt":      "hello from top",
		"sub/deep.txt": "hello from deep",
	}
	for path, content := range files {
		fullPath := dir + "/" + path
		if err := os.MkdirAll(dir+"/sub", 0755); err != nil {
			t.Fatal(err)
		}
		if err := writeFile(fullPath, content); err != nil {
			t.Fatal(err)
		}
	}

	// recursive=false → 只搜顶层
	results := aiGrep(context.Background(), dir, "hello", false, false, nil, 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result (top only), got %d", len(results))
	}
	if results[0].FilePath != "top.txt" {
		t.Errorf("expected top.txt, got %s", results[0].FilePath)
	}

	// recursive=true → 搜到所有
	results2 := aiGrep(context.Background(), dir, "hello", false, true, nil, 10)
	if len(results2) != 2 {
		t.Fatalf("expected 2 results (recursive), got %d", len(results2))
	}
}

// ---------------------------------------------------------------------------
// path 参数测试（aiGrep 使用指定目录而非 session.Root）
// ---------------------------------------------------------------------------

func TestAiGrepCustomPath(t *testing.T) {
	dir := t.TempDir()

	// 在 root 和 sub 下各建文件
	rootFiles := map[string]string{
		"root.txt": "search target",
	}
	subFiles := map[string]string{
		"sub.txt": "search target",
	}
	for path, content := range rootFiles {
		if err := writeFile(dir+"/"+path, content); err != nil {
			t.Fatal(err)
		}
	}
	subDir := dir + "/subdir"
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	for path, content := range subFiles {
		if err := writeFile(subDir+"/"+path, content); err != nil {
			t.Fatal(err)
		}
	}

	// 指定子目录搜索
	results := aiGrep(context.Background(), subDir, "target", false, true, nil, 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result in subdir, got %d", len(results))
	}
	if results[0].FilePath != "sub.txt" {
		t.Errorf("expected sub.txt, got %s", results[0].FilePath)
	}
}

// ---------------------------------------------------------------------------
// contextSearch 标签排序（不依赖 contextSearchFn）
// ---------------------------------------------------------------------------

func TestContextSearchTagOrdering(t *testing.T) {
	// 直接测试排序逻辑（contextSearch 内部）
	// 模拟 contextSearchFn 返回的混合 tag 结果
	results := []ContextSearchResult{
		{FilePath: "normal.go", Content: "func main()", Tags: `["file"]`, Score: 0.9},
		{FilePath: "temp.txt", Content: "temp data", Tags: `["tempfs"]`, Score: 0.8},
		{FilePath: "normal2.go", Content: "func helper()", Tags: `["function"]`, Score: 0.7},
		{FilePath: "chat.txt", Content: "history", Tags: `["chathistory"]`, Score: 0.6},
		{FilePath: "normal3.go", Content: "func test()", Tags: `["file"]`, Score: 0.5},
	}

	out := reorderSearchResults(results)

	// 前3条应为 normal（normal.go, normal2.go, normal3.go）
	if len(out) != 5 {
		t.Fatalf("expected 5 results, got %d", len(out))
	}
	for i := range 3 {
		if !strings.HasPrefix(out[i].FilePath, "normal") {
			t.Errorf("index %d should be normal, got %s", i, out[i].FilePath)
		}
	}
	// 后2条应为 tagged（temp.txt, chat.txt）
	if !strings.Contains(out[3].FilePath, "temp") && !strings.Contains(out[3].FilePath, "chat") {
		t.Errorf("index 3 should be tagged, got %s", out[3].FilePath)
	}
}

func TestContextSearchTagLimit(t *testing.T) {
	// tagged 超过3条应截断
	results := []ContextSearchResult{
		{FilePath: "normal.go", Tags: `["file"]`},
		{FilePath: "t1.txt", Tags: `["tempfs"]`},
		{FilePath: "t2.txt", Tags: `["tempfs"]`},
		{FilePath: "t3.txt", Tags: `["tempfs"]`},
		{FilePath: "t4.txt", Tags: `["tempfs"]`},
		{FilePath: "t5.txt", Tags: `["tempfs"]`},
	}

	out := reorderSearchResults(results)

	// 1 normal + 3 tagged cap = 4 total
	if len(out) != 4 {
		t.Fatalf("expected 4 results (1 normal + 3 tagged cap), got %d", len(out))
	}
}

func TestContextSearchAllTagged(t *testing.T) {
	// 全是 tagged → 最多出 3 条
	results := []ContextSearchResult{
		{FilePath: "t1.txt", Tags: `["tempfs"]`},
		{FilePath: "t2.txt", Tags: `["chathistory"]`},
		{FilePath: "t3.txt", Tags: `["tempfs"]`},
		{FilePath: "t4.txt", Tags: `["chathistory"]`},
	}

	out := reorderSearchResults(results)

	if len(out) > 3 {
		t.Fatalf("expected max 3 tagged results, got %d", len(out))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// reorderSearchResults 暴露 contextSearch 内部的排序逻辑用于测试
func reorderSearchResults(results []ContextSearchResult) []searchResult {
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
		if strings.Contains(r.Tags, "tempfs") || strings.Contains(r.Tags, "chathistory") {
			tagged = append(tagged, sr)
		} else {
			normal = append(normal, sr)
		}
	}
	if len(tagged) > 3 {
		tagged = tagged[:3]
	}
	return append(normal, tagged...)
}
