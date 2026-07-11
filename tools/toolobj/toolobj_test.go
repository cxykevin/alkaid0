package toolobj

import (
	"sync"
	"testing"
)

// setupCleanState 清理全局状态并返回恢复函数
func setupCleanState() func() {
	oldTools := ToolsList
	oldScopes := Scopes
	ToolsList = make(map[string]*Tools)
	Scopes = make(map[string]string)
	return func() {
		ToolsList = oldTools
		Scopes = oldScopes
	}
}

func TestToolsListInit(t *testing.T) {
	if ToolsList == nil {
		t.Error("ToolsList should be initialized")
	}
}

func TestScopesInit(t *testing.T) {
	if Scopes == nil {
		t.Error("Scopes should be initialized")
	}
}

func TestSetAndGetScope(t *testing.T) {
	defer setupCleanState()()

	// 设置 Scope
	SetScope("test-scope", "scope prompt")
	v, ok := GetScope("test-scope")
	if !ok {
		t.Error("GetScope should return ok=true for existing scope")
	}
	if v != "scope prompt" {
		t.Errorf("GetScope prompt = %q, want %q", v, "scope prompt")
	}

	// 获取不存在的 Scope
	_, ok = GetScope("non-existent")
	if ok {
		t.Error("GetScope should return ok=false for non-existent scope")
	}

	// 覆盖 Scope
	SetScope("test-scope", "updated prompt")
	v, ok = GetScope("test-scope")
	if !ok {
		t.Error("GetScope should return ok=true for updated scope")
	}
	if v != "updated prompt" {
		t.Errorf("GetScope prompt = %q, want %q", v, "updated prompt")
	}
}

func TestSetAndGetTool(t *testing.T) {
	defer setupCleanState()()

	// 设置工具
	tool := &Tools{
		Scope: "global",
		Name:  "test_tool",
		ID:    "test_tool",
	}
	SetTool(tool)

	// 获取存在的工具
	got := GetTool("test_tool")
	if got == nil {
		t.Fatal("GetTool should return non-nil for existing tool")
	}
	if got.Name != "test_tool" {
		t.Errorf("Tool name = %q, want %q", got.Name, "test_tool")
	}
	if got.Scope != "global" {
		t.Errorf("Tool scope = %q, want %q", got.Scope, "global")
	}

	// 获取不存在的工具
	got = GetTool("non-existent")
	if got != nil {
		t.Error("GetTool should return nil for non-existent tool")
	}
}

func TestGetToolHooks(t *testing.T) {
	defer setupCleanState()()

	// 无钩子的工具
	tool := &Tools{
		Name: "no_hooks",
		ID:   "no_hooks",
	}
	SetTool(tool)

	hooks := GetToolHooks("no_hooks")
	if hooks == nil {
		t.Error("GetToolHooks should return empty slice, not nil")
	}
	if len(hooks) != 0 {
		t.Errorf("Expected 0 hooks, got %d", len(hooks))
	}

	// 有钩子的工具
	hooks = []Hook{
		{Scope: "global"},
		{Scope: "local"},
	}
	toolWithHooks := &Tools{
		Name:  "has_hooks",
		ID:    "has_hooks",
		Hooks: hooks,
	}
	SetTool(toolWithHooks)

	hooks = GetToolHooks("has_hooks")
	if len(hooks) != 2 {
		t.Fatalf("Expected 2 hooks, got %d", len(hooks))
	}

	// 验证返回的是副本（修改不应影响原值）
	hooks[0].Scope = "modified"
	originalHooks := GetToolHooks("has_hooks")
	if originalHooks[0].Scope != "global" {
		t.Error("GetToolHooks should return a copy, not the original slice")
	}

	// 不存在的工具
	hooks = GetToolHooks("non-existent")
	if hooks != nil {
		t.Error("GetToolHooks should return nil for non-existent tool")
	}
}

func TestAppendToolHook(t *testing.T) {
	defer setupCleanState()()

	// 追加到不存在的工具
	ok := AppendToolHook("non-existent", Hook{Scope: "global"})
	if ok {
		t.Error("AppendToolHook should return false for non-existent tool")
	}

	// 追加到存在的工具
	tool := &Tools{
		Name: "test_tool",
		ID:   "test_tool",
	}
	SetTool(tool)

	ok = AppendToolHook("test_tool", Hook{Scope: "global"})
	if !ok {
		t.Error("AppendToolHook should return true for existing tool")
	}

	hooks := GetToolHooks("test_tool")
	if len(hooks) != 1 {
		t.Fatalf("Expected 1 hook after append, got %d", len(hooks))
	}
	if hooks[0].Scope != "global" {
		t.Errorf("Hook scope = %q, want %q", hooks[0].Scope, "global")
	}

	// 多次追加
	AppendToolHook("test_tool", Hook{Scope: "local"})
	AppendToolHook("test_tool", Hook{Scope: "priority"})

	hooks = GetToolHooks("test_tool")
	if len(hooks) != 3 {
		t.Fatalf("Expected 3 hooks, got %d", len(hooks))
	}
}

func TestConcurrentAccess(t *testing.T) {
	defer setupCleanState()()

	const goroutines = 10
	var wg sync.WaitGroup

	// 先注册一个工具
	SetTool(&Tools{Name: "conc_tool", ID: "conc_tool"})

	// 并发读写
	wg.Add(goroutines * 2)
	for range goroutines {
		go func() {
			defer wg.Done()
			SetScope("scope", "prompt")
		}()
		go func() {
			defer wg.Done()
			GetTool("conc_tool")
		}()
	}
	wg.Wait()

	// 验证读写锁没有造成死锁
	v, ok := GetScope("scope")
	if !ok {
		t.Error("Scope should exist after concurrent writes")
	}
	if v != "prompt" {
		t.Errorf("Scope prompt = %q, want %q", v, "prompt")
	}
}

func TestSetToolOverwritesExisting(t *testing.T) {
	defer setupCleanState()()

	tool1 := &Tools{Name: "overwrite", ID: "overwrite", Scope: "v1"}
	tool2 := &Tools{Name: "overwrite", ID: "overwrite", Scope: "v2"}

	SetTool(tool1)
	SetTool(tool2)

	got := GetTool("overwrite")
	if got.Scope != "v2" {
		t.Errorf("Tool scope = %q, want %q after overwrite", got.Scope, "v2")
	}
}

func TestGetToolHooksAfterToolRemoval(t *testing.T) {
	defer setupCleanState()()

	// 注册后手动移除来模拟删除场景
	SetTool(&Tools{Name: "removed", ID: "removed"})

	delete(ToolsList, "removed")

	hooks := GetToolHooks("removed")
	if hooks != nil {
		t.Error("GetToolHooks should return nil for removed tool")
	}
}

func TestToolsAndScopesIndependence(t *testing.T) {
	defer setupCleanState()()

	// 验证工具和 Scope 是独立的存储
	SetTool(&Tools{Name: "tool1", ID: "tool1", Scope: "scope1"})
	SetScope("scope1", "scope description")

	// 删除 Scope 不应影响工具
	delete(Scopes, "scope1")
	got := GetTool("tool1")
	if got == nil || got.Name != "tool1" {
		t.Error("Tool should still exist after scope removal")
	}

	// 删除工具不应影响 Scope
	SetScope("scope2", "desc")
	delete(ToolsList, "tool1")
	_, ok := GetScope("scope2")
	if !ok {
		t.Error("Scope should still exist after tool removal")
	}
}
