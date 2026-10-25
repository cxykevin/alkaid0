package trace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/context/outline"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

func TestIsLargeFileContent(t *testing.T) {
	if isLargeFileContent("small\nfile\n") {
		t.Error("小文件不应判为大文件")
	}
	big := strings.Repeat("line\n", readLightMinLines+1)
	if !isLargeFileContent(big) {
		t.Error("超过行数阈值应判为大文件")
	}
	if !isLargeFileContent(strings.Repeat("x", readLightMinBytes)) {
		t.Error("超过字节阈值应判为大文件")
	}
}

func TestRenderOutline(t *testing.T) {
	syms := []OutlineSymbol{
		{Name: "Second", Kind: "method", Signature: "func (c *C) Second()\nerror", Line: 200},
		{Name: "", Kind: "function", Line: 1}, // 空名跳过
		{Name: "First", Kind: "function", Signature: "func First(  a,\n b int ) int", Line: 10},
		{Name: "First", Kind: "function", Line: 10}, // 重复行去重
	}
	got := renderOutline(syms)
	if strings.Contains(got, "Second") == false || strings.Index(got, "First") > strings.Index(got, "Second") {
		t.Errorf("应按行号升序渲染: %q", got)
	}
	if strings.Count(got, "First") != 1 {
		t.Errorf("同一行同名符号应去重: %q", got)
	}
	if !strings.Contains(got, "func First( a, b int ) int") {
		t.Errorf("签名应压缩空白且只取首行: %q", got)
	}
	if !strings.Contains(got, "L200\tmethod\tfunc (c *C) Second()") {
		t.Errorf("多行签名只保留首行: %q", got)
	}
	// 无签名时退回符号名
	if !strings.Contains(got, "L200") {
		t.Fatalf("unexpected outline: %q", got)
	}

	many := make([]OutlineSymbol, outlineMaxSymbols+5)
	for i := range many {
		many[i] = OutlineSymbol{Name: fmt.Sprintf("F%03d", i), Kind: "function", Line: uint32(i + 1)}
	}
	capped := renderOutline(many)
	if !strings.Contains(capped, "... (5 more symbols)") {
		t.Errorf("超过上限应提示剩余数量: %q", capped)
	}
	if strings.Count(capped, "L") != outlineMaxSymbols {
		t.Errorf("最多渲染 %d 个符号, got %d", outlineMaxSymbols, strings.Count(capped, "L"))
	}
}

func TestExcerptOf(t *testing.T) {
	// 行数上限
	content := strings.Repeat("line\n", excerptKeepLines+50)
	kept, omitted := excerptOf(content)
	if strings.Count(kept, "\n")+1 > excerptKeepLines {
		t.Errorf("截断后行数超上限: %d", strings.Count(kept, "\n")+1)
	}
	if omitted <= 0 {
		t.Errorf("应统计被省略的行数, got %d", omitted)
	}
	// 字节上限：单行超长时至少保留一行
	hugeLine := strings.Repeat("x", excerptKeepBytes*2)
	kept, _ = excerptOf(hugeLine)
	if kept == "" {
		t.Error("超长单行也应保留至少一行")
	}
	// 内容本身很短：全量保留、不报省略
	kept, omitted = excerptOf("a\nb")
	if kept != "a\nb" || omitted != 0 {
		t.Errorf("短内容不应截断: kept=%q omitted=%d", kept, omitted)
	}
}

func TestInjectReadView(t *testing.T) {
	session := &structs.Chats{TemporyDataOfSession: map[string]any{}}
	content := strings.Repeat("line\n", 500)

	// 未注册 provider → 截断视图
	restore := stubOutlineProvider(nil)
	text := injectReadView(session, t.TempDir(), "big.go", ReadModeLight, content)
	if !strings.HasPrefix(text, lightViewMarker+" excerpt]") {
		t.Errorf("无 provider 时应退化为截断视图: %q", text[:40])
	}
	if !strings.Contains(text, "more lines omitted") {
		t.Errorf("截断视图应说明省略行数")
	}
	restore()

	// provider 报错 → 仍截断
	restore = stubOutlineProvider(func(string, string) ([]OutlineSymbol, error) {
		return nil, fmt.Errorf("no lsp")
	})
	if text := injectReadView(session, t.TempDir(), "big2.go", ReadModeLight, content); !strings.Contains(text, "excerpt]") {
		t.Errorf("provider 报错应退化为截断视图: %q", text[:40])
	}
	restore()

	// provider 正常 → 大纲视图
	restore = stubOutlineProvider(func(string, string) ([]OutlineSymbol, error) {
		return []OutlineSymbol{{Name: "Big", Kind: "function", Signature: "func Big()", Line: 12}}, nil
	})
	defer restore()
	out := injectReadView(session, t.TempDir(), "big3.go", ReadModeLight, content)
	if !strings.HasPrefix(out, lightViewMarker+" outline]") {
		t.Fatalf("应渲染大纲视图: %q", out[:40])
	}
	if !strings.Contains(out, "L12\tfunction\tfunc Big()") {
		t.Errorf("大纲应包含符号与行号: %q", out)
	}
	if !strings.Contains(out, "search") {
		t.Errorf("大纲应引导模型使用 search: %q", out)
	}

	// 非 light 模式 → 原样返回全文
	if got := injectReadView(session, t.TempDir(), "big3.go", "", content); got != content {
		t.Error("非 light 模式必须返回全文")
	}
}

// TestReadLargeFileOutlineThenFull 验证完整流程：
// 大文件首读注入大纲（受限视图，块不加行号），同一文件再次 read 注入全量。
func TestReadLargeFileOutlineThenFull(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	defer outline.CloseAll()

	tmpDir := t.TempDir()
	var content strings.Builder
	for i := range 500 {
		fmt.Fprintf(&content, "line %03d of the big source file\n", i)
	}
	filePath := filepath.Join(tmpDir, "big.go")
	if err := os.WriteFile(filePath, []byte(content.String()), 0644); err != nil {
		t.Fatalf("write big.go: %v", err)
	}

	restore := stubOutlineProvider(func(workdir, absPath string) ([]OutlineSymbol, error) {
		return []OutlineSymbol{
			{Name: "Big", Kind: "function", Signature: "func Big() int", Line: 12},
			{Name: "Other", Kind: "method", Signature: "func (t *T) Other()", Line: 300},
		}, nil
	})
	defer restore()

	session := &structs.Chats{
		ID:       1,
		DB:       db,
		NowAgent: "test_agent",
		Root:     tmpDir,
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
				"big.go": {MsgID: 1, ToolCallID: "call_1"},
			},
		},
	}

	// 首次 read：写入 light 模式，返回文案引导 search / 再次 read
	pathAny := any("big.go")
	_, _, result, err := Trace(session, map[string]*any{"path": &pathAny}, nil)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	if msg, _ := (*result["message"]).(string); !strings.Contains(msg, "structural outline") {
		t.Errorf("大文件首读的返回文案应说明注入的是大纲: %q", msg)
	}
	var row structs.Traces
	if err := db.Where("chat_id = 1 AND path = 'big.go'").First(&row).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if row.ReadMode != ReadModeLight {
		t.Fatalf("大文件首读应标记 light 模式, got %q", row.ReadMode)
	}

	// 首次渲染：大纲块直出（不加行号），内容来自 provider
	_, blocks, err := RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}
	block, ok := blocks["big.go"]
	if !ok {
		t.Fatal("缺少 big.go 内容块")
	}
	if block.Type != "outline" {
		t.Errorf("首次注入的块类型应为 outline, got %q", block.Type)
	}
	if !strings.Contains(block.Text, "L12\tfunction\tfunc Big() int") {
		t.Errorf("大纲块缺少符号行: %q", block.Text)
	}
	if strings.Contains(block.Text, "|line 012") {
		t.Error("大纲块不应被二次加行号")
	}

	// 再次 read：升级为全量
	_, _, result, err = Trace(session, map[string]*any{"path": &pathAny}, nil)
	if err != nil {
		t.Fatalf("second Trace: %v", err)
	}
	if msg, _ := (*result["message"]).(string); strings.Contains(msg, "structural outline") {
		t.Errorf("再次 read 应返回全量文案, got %q", msg)
	}
	if err := db.Where("chat_id = 1 AND path = 'big.go'").First(&row).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if row.ReadMode != "" {
		t.Fatalf("再次 read 应清空 light 模式, got %q", row.ReadMode)
	}

	// 再次渲染：全量内容（带行号）
	_, blocks, err = RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}
	block, ok = blocks["big.go"]
	if !ok {
		t.Fatal("缺少 big.go 内容块")
	}
	if block.Type != "" {
		t.Errorf("全量块不应带受限类型, got %q", block.Type)
	}
	if !strings.Contains(block.Text, "line 499 of the big source file") {
		t.Errorf("再次 read 后应注入全量内容: %.200q", block.Text)
	}

	// diff 基线必须等于实际注入的字节（全量），而不是大纲
	if err := db.Where("chat_id = 1 AND path = 'big.go'").First(&row).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if row.LastContent != content.String() {
		t.Errorf("全量注入后基线应为全文, got %d bytes want %d", len(row.LastContent), len(content.String()))
	}
}

// TestReadSmallFileStaysFull 小文件不参与受限视图，且不受历史 light 标记影响。
func TestReadSmallFileStaysFull(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "small.go"), []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("write small.go: %v", err)
	}

	// 即使历史行残留 light 标记，小文件也应恢复全量
	if err := db.Create(&structs.Traces{ChatID: 1, Path: "small.go", TraceID: 1, AgentID: "test_agent",
		ReadMode: ReadModeLight, LastContent: "stale"}).Error; err != nil {
		t.Fatalf("seed trace: %v", err)
	}

	session := &structs.Chats{ID: 1, DB: db, NowAgent: "test_agent", Root: tmpDir,
		TemporyDataOfSession: map[string]any{}}
	pathAny := any("small.go")
	if _, _, _, err := Trace(session, map[string]*any{"path": &pathAny}, nil); err != nil {
		t.Fatalf("Trace: %v", err)
	}

	var row structs.Traces
	if err := db.Where("chat_id = 1 AND path = 'small.go'").First(&row).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if row.ReadMode != "" {
		t.Errorf("小文件应清空 light 标记, got %q", row.ReadMode)
	}
}

// stubOutlineProvider 替换全局 provider，返回恢复函数。
func stubOutlineProvider(fn func(workdir, absPath string) ([]OutlineSymbol, error)) func() {
	prev := outlineProvider
	SetOutlineProvider(fn)
	return func() { SetOutlineProvider(prev) }
}

// TestInjectReadViewPrefersPersistedOutline 已落库且未过期的大纲优先于现场 LSP，
// 并在块内给出缓存时间（生成时间 + 文件 mtime）供模型判断是否过期。
func TestInjectReadViewPrefersPersistedOutline(t *testing.T) {
	tmpDir := t.TempDir()
	defer outline.CloseAll()
	content := strings.Repeat("line\n", 500)
	if err := os.WriteFile(filepath.Join(tmpDir, "cached.go"), []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := outline.Put(tmpDir, "cached.go", []outline.Symbol{
		{Name: "Cached", Kind: "function", Signature: "func Cached()", Line: 7},
	}); !ok {
		t.Fatal("Put 持久大纲失败")
	}

	called := false
	restore := stubOutlineProvider(func(string, string) ([]OutlineSymbol, error) {
		called = true
		return nil, errors.New("provider should not be called")
	})
	defer restore()

	session := &structs.Chats{TemporyDataOfSession: map[string]any{}, Root: tmpDir}
	out := injectReadView(session, tmpDir, "cached.go", ReadModeLight, content)
	if called {
		t.Error("命中持久大纲时不应再调用 LSP")
	}
	if !strings.HasPrefix(out, lightViewMarker+" outline]") {
		t.Fatalf("应为大纲视图: %.80q", out)
	}
	if !strings.Contains(out, "L7\tfunction\tfunc Cached()") {
		t.Errorf("大纲应来自持久缓存: %q", out)
	}
	if !strings.Contains(out, "Outline cached at") || !strings.Contains(out, "file mtime") {
		t.Errorf("缺少缓存时间信息: %q", out)
	}
}

// TestInjectReadViewRefreshesStaleOutline 文件 mtime 变化后旧大纲视为过期，
// 回退现场 LSP 并把新大纲写回缓存。
func TestInjectReadViewRefreshesStaleOutline(t *testing.T) {
	tmpDir := t.TempDir()
	defer outline.CloseAll()
	content := strings.Repeat("line\n", 500)
	abs := filepath.Join(tmpDir, "stale.go")
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := outline.Put(tmpDir, "stale.go", []outline.Symbol{{Name: "Old", Line: 3}}); !ok {
		t.Fatal("Put 持久大纲失败")
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(abs, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	restore := stubOutlineProvider(func(string, string) ([]OutlineSymbol, error) {
		return []OutlineSymbol{{Name: "New", Kind: "function", Line: 9}}, nil
	})
	defer restore()

	session := &structs.Chats{TemporyDataOfSession: map[string]any{}, Root: tmpDir}
	out := injectReadView(session, tmpDir, "stale.go", ReadModeLight, content)
	if strings.Contains(out, "Old") {
		t.Errorf("过期大纲不应被注入: %q", out)
	}
	if !strings.Contains(out, "L9\tfunction\tNew") {
		t.Errorf("应使用现场 LSP 的新大纲: %q", out)
	}
	rec, ok := outline.Get(tmpDir, "stale.go")
	if !ok || !strings.Contains(rec.Text, "New") {
		t.Errorf("新大纲应写回缓存, ok=%v text=%q", ok, rec.Text)
	}
}

// TestRefreshFileIndexWithSymbols 带符号刷新直接调用注入实现，不再触发 LSP，
// 内容与符号原样传递给索引层（edit 复用它合并两次 documentSymbol）。
func TestRefreshFileIndexWithSymbols(t *testing.T) {
	prev, prevDisk := fileIndexRefresher, fileIndexRefresherFromDisk
	defer func() { fileIndexRefresher, fileIndexRefresherFromDisk = prev, prevDisk }()

	var gotContent, gotPath string
	var gotSymbols []FileIndexSymbol
	fileIndexRefresher = func(workdir, relPath, content string, symbols []FileIndexSymbol) error {
		gotPath, gotContent, gotSymbols = relPath, content, symbols
		return nil
	}

	providerCalled := false
	restore := stubOutlineProvider(func(string, string) ([]OutlineSymbol, error) {
		providerCalled = true
		return nil, errors.New("provider should not be called")
	})
	defer restore()

	syms := []FileIndexSymbol{{Name: "A", Kind: "function", Signature: "func A()", Code: "func A() {}"}}
	RefreshFileIndexWithSymbols("/w", "a.go", "package a\n", syms)

	if providerCalled {
		t.Error("带符号刷新不应再调用 LSP")
	}
	if gotPath != "a.go" || gotContent != "package a\n" {
		t.Errorf("注入实现未收到路径/内容: %q %q", gotPath, gotContent)
	}
	if len(gotSymbols) != 1 || gotSymbols[0].Name != "A" || gotSymbols[0].Code != "func A() {}" {
		t.Errorf("注入实现未收到符号分块: %+v", gotSymbols)
	}
}
