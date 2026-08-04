package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

func strPtr(s string) *any {
	a := any(s)
	return &a
}

// setGlobalMemoryDir 覆写 globalMemoryDir 到临时目录并注册恢复
func setGlobalMemoryDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := globalMemoryDir
	globalMemoryDir = func() string { return dir }
	t.Cleanup(func() { globalMemoryDir = old })
	return dir
}

// disableUserHomeDir 覆写 userHomeDir 为空（禁用 home 边界检查），便于测纯向上查找
func disableUserHomeDir(t *testing.T) {
	t.Helper()
	old := userHomeDir
	userHomeDir = func() string { return "" }
	t.Cleanup(func() { userHomeDir = old })
}

func resultSuccess(resultMap map[string]*any) bool {
	successPtr, ok := resultMap["success"]
	if !ok {
		return false
	}
	successBool, ok := (*successPtr).(bool)
	return ok && successBool
}

func resultError(resultMap map[string]*any) string {
	errPtr, ok := resultMap["error"]
	if !ok {
		return ""
	}
	s, _ := (*errPtr).(string)
	return s
}

// TestWriteMemory_PathNotMemory 路径不是 @memory/@memory/global 时放行
func TestWriteMemory_PathNotMemory(t *testing.T) {
	session := &structs.Chats{}
	mp := map[string]*any{
		"path": strPtr("some_file.txt"),
	}
	success, _, resultMap, _ := writeMemory(session, mp, []*any{})
	if !success {
		t.Fatalf("expected pass=true for non-memory path")
	}
	if resultMap != nil {
		t.Fatalf("expected nil result map for non-memory path")
	}
}

// TestWriteMemory_MissingPath 缺少 path 参数时放行
func TestWriteMemory_MissingPath(t *testing.T) {
	session := &structs.Chats{}
	success, _, resultMap, _ := writeMemory(session, map[string]*any{}, []*any{})
	if !success {
		t.Fatalf("expected pass=true on path error")
	}
	if resultMap != nil {
		t.Fatalf("expected nil result map on path error")
	}
}

// TestWriteMemory_MissingTargetText 缺少 target/text 参数
func TestWriteMemory_MissingTargetText(t *testing.T) {
	session := &structs.Chats{}
	mp := map[string]*any{
		"path": strPtr("@memory"),
	}
	success, _, resultMap, _ := writeMemory(session, mp, []*any{})
	if success {
		t.Fatalf("expected pass=false when missing target/text")
	}
	if resultMap == nil {
		t.Fatalf("expected result map with error")
	}
	if resultError(resultMap) == "" {
		t.Fatalf("expected error in result map")
	}
}

// TestWriteMemory_Append_Project 追加写入项目级 @memory
func TestWriteMemory_Append_Project(t *testing.T) {
	root := t.TempDir()
	session := &structs.Chats{Root: root}
	mp := map[string]*any{
		"path":   strPtr("@memory"),
		"target": strPtr(""),
		"text":   strPtr("- 项目约定: 使用纯 Go 依赖"),
	}
	pass, _, resultMap, _ := writeMemory(session, mp, []*any{})
	if pass {
		t.Fatalf("expected pass=false for @memory")
	}
	if !resultSuccess(resultMap) {
		t.Fatalf("expected success=true in result")
	}

	memPath := filepath.Join(root, ".alkaid0", "MEMORY.md")
	data, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("memory file not written: %v", err)
	}
	if !strings.Contains(string(data), "- 项目约定: 使用纯 Go 依赖") {
		t.Fatalf("memory content mismatch: %q", string(data))
	}
}

// TestWriteMemory_All_Project @all 替换已有内容
func TestWriteMemory_All_Project(t *testing.T) {
	root := t.TempDir()
	memPath := filepath.Join(root, ".alkaid0", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memPath, []byte("旧内容\n"), 0644); err != nil {
		t.Fatal(err)
	}
	session := &structs.Chats{Root: root}
	mp := map[string]*any{
		"path":   strPtr("@memory"),
		"target": strPtr("@all"),
		"text":   strPtr("- 新的约定"),
	}
	pass, _, resultMap, _ := writeMemory(session, mp, []*any{})
	if pass || !resultSuccess(resultMap) {
		t.Fatalf("expected success=false pass=%v result=%v", pass, resultMap)
	}
	data, _ := os.ReadFile(memPath)
	if strings.Contains(string(data), "旧内容") {
		t.Fatalf("expected old content replaced, got %q", string(data))
	}
	if !strings.Contains(string(data), "- 新的约定") {
		t.Fatalf("expected new content, got %q", string(data))
	}
}

// TestWriteMemory_SubstringReplace 已有文件 + 子串替换
func TestWriteMemory_SubstringReplace(t *testing.T) {
	root := t.TempDir()
	memPath := filepath.Join(root, ".alkaid0", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memPath, []byte("- 使用 pure-Go 依赖\n"), 0644); err != nil {
		t.Fatal(err)
	}
	session := &structs.Chats{Root: root}
	mp := map[string]*any{
		"path":   strPtr("@memory"),
		"target": strPtr("pure-Go"),
		"text":   strPtr("pure-Go (无 cgo)"),
	}
	pass, _, resultMap, _ := writeMemory(session, mp, []*any{})
	if pass || !resultSuccess(resultMap) {
		t.Fatalf("expected success, pass=%v", pass)
	}
	data, _ := os.ReadFile(memPath)
	if !strings.Contains(string(data), "pure-Go (无 cgo)") {
		t.Fatalf("expected substring replaced, got %q", string(data))
	}
}

// TestWriteMemory_NewFile_SubstringError 文件不存在 + 子串 target → 报错
func TestWriteMemory_NewFile_SubstringError(t *testing.T) {
	root := t.TempDir()
	session := &structs.Chats{Root: root}
	mp := map[string]*any{
		"path":   strPtr("@memory"),
		"target": strPtr("不存在的内容"),
		"text":   strPtr("新内容"),
	}
	pass, _, resultMap, _ := writeMemory(session, mp, []*any{})
	if pass {
		t.Fatalf("expected pass=false for invalid new-file edit")
	}
	if resultError(resultMap) == "" {
		t.Fatalf("expected error for invalid new-file edit")
	}
	// 不应创建文件
	if _, err := os.Stat(filepath.Join(root, ".alkaid0", "MEMORY.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no file created, got err=%v", err)
	}
}

// TestWriteMemory_Global 写入全局 @memory/global（配置文件同目录）
func TestWriteMemory_Global(t *testing.T) {
	dir := setGlobalMemoryDir(t)
	session := &structs.Chats{}
	mp := map[string]*any{
		"path":   strPtr("@memory/global"),
		"target": strPtr(""),
		"text":   strPtr("- 全局偏好: 中文回复"),
	}
	pass, _, resultMap, _ := writeMemory(session, mp, []*any{})
	if pass || !resultSuccess(resultMap) {
		t.Fatalf("expected success, pass=%v", pass)
	}
	memPath := filepath.Join(dir, "MEMORY.md")
	data, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("global memory file not written: %v", err)
	}
	if !strings.Contains(string(data), "- 全局偏好: 中文回复") {
		t.Fatalf("global memory content mismatch: %q", string(data))
	}
}

// TestWriteMemory_MkdirAll Root 下无 .alkaid0 目录时自动创建
func TestWriteMemory_MkdirAll(t *testing.T) {
	root := t.TempDir() // 无 .alkaid0
	session := &structs.Chats{Root: root}
	mp := map[string]*any{
		"path":   strPtr("@memory"),
		"target": strPtr(""),
		"text":   strPtr("hello"),
	}
	pass, _, resultMap, _ := writeMemory(session, mp, []*any{})
	if pass || !resultSuccess(resultMap) {
		t.Fatalf("expected success, pass=%v", pass)
	}
	if fi, err := os.Stat(filepath.Join(root, ".alkaid0", "MEMORY.md")); err != nil || fi.IsDir() {
		t.Fatalf("expected .alkaid0/MEMORY.md created, err=%v", err)
	}
}

// TestBuildMemoryPrompt_Empty 无任何 memory 文件 → 返回非空引导语
func TestBuildMemoryPrompt_Empty(t *testing.T) {
	// 隔离全局 memory：不依赖默认配置目录（CI 上 ALKAID0_CONFIG_PATH 指向 cwd，
	// 大小写不敏感文件系统会把模板 memory.md 误读为全局 MEMORY.md）
	setGlobalMemoryDir(t)
	session := &structs.Chats{Root: t.TempDir()}
	out, err := buildMemoryPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatalf("expected non-empty guidance for empty memory")
	}
	if !strings.Contains(out, "@memory") {
		t.Fatalf("expected @memory mentioned in guidance, got %q", out)
	}
}

// TestBuildMemoryPrompt_ProjectOnly 仅项目 memory → 输出含内容且不含 Global 段
func TestBuildMemoryPrompt_ProjectOnly(t *testing.T) {
	// 隔离全局 memory，避免 CI 环境耦合（同 TestBuildMemoryPrompt_Empty 注释）
	setGlobalMemoryDir(t)
	root := t.TempDir()
	memPath := filepath.Join(root, ".alkaid0", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memPath, []byte("- 项目记忆"), 0644); err != nil {
		t.Fatal(err)
	}
	session := &structs.Chats{Root: root}
	out, err := buildMemoryPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "- 项目记忆") {
		t.Fatalf("expected project memory content, got %q", out)
	}
	if strings.Contains(out, "Global Memory") {
		t.Fatalf("expected no Global Memory section, got %q", out)
	}
}

// TestBuildMemoryPrompt_GlobalOnly 仅全局 memory → 输出含全局内容
func TestBuildMemoryPrompt_GlobalOnly(t *testing.T) {
	dir := setGlobalMemoryDir(t)
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- 全局记忆"), 0644); err != nil {
		t.Fatal(err)
	}
	session := &structs.Chats{Root: t.TempDir()}
	out, err := buildMemoryPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "- 全局记忆") {
		t.Fatalf("expected global memory content, got %q", out)
	}
}

// TestBuildMemoryPrompt_Both 两者都有 → 输出含两段
func TestBuildMemoryPrompt_Both(t *testing.T) {
	root := t.TempDir()
	memPath := filepath.Join(root, ".alkaid0", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memPath, []byte("- 项目记忆"), 0644); err != nil {
		t.Fatal(err)
	}
	dir := setGlobalMemoryDir(t)
	if err := os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- 全局记忆"), 0644); err != nil {
		t.Fatal(err)
	}
	session := &structs.Chats{Root: root}
	out, err := buildMemoryPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "- 项目记忆") || !strings.Contains(out, "- 全局记忆") {
		t.Fatalf("expected both memories, got %q", out)
	}
}

// TestBuildAgentsPrompt_None 无 AGENTS.md/CLAUDE.md → 返回空串
func TestBuildAgentsPrompt_None(t *testing.T) {
	disableUserHomeDir(t)
	session := &structs.Chats{Root: t.TempDir()}
	out, err := buildAgentsPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output when no instruction files, got %q", out)
	}
}

// TestBuildAgentsPrompt_Found 工作目录有 AGENTS.md + CLAUDE.md → 输出含两文件内容
func TestBuildAgentsPrompt_Found(t *testing.T) {
	disableUserHomeDir(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agents 内容"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("claude 内容"), 0644); err != nil {
		t.Fatal(err)
	}
	session := &structs.Chats{Root: root}
	out, err := buildAgentsPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "agents 内容") {
		t.Fatalf("expected AGENTS.md content, got %q", out)
	}
	if !strings.Contains(out, "claude 内容") {
		t.Fatalf("expected CLAUDE.md content, got %q", out)
	}
}

// TestResolveMemoryPath 虚拟路径解析
func TestResolveMemoryPath(t *testing.T) {
	session := &structs.Chats{Root: "/proj"}
	if p, err := resolveMemoryPath(session, "@memory"); err != nil || p != filepath.Join("/proj", ".alkaid0", "MEMORY.md") {
		t.Fatalf("unexpected @memory path %q err=%v", p, err)
	}
	dir := setGlobalMemoryDir(t)
	if p, err := resolveMemoryPath(session, "@memory/global"); err != nil || p != filepath.Join(dir, "MEMORY.md") {
		t.Fatalf("unexpected @memory/global path %q err=%v", p, err)
	}
	if _, err := resolveMemoryPath(session, "@unknown"); err == nil {
		t.Fatalf("expected error for unknown path")
	}
}

// TestFindUpward_CurrentDir 当前目录命中
func TestFindUpward_CurrentDir(t *testing.T) {
	disableUserHomeDir(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := findUpward(root, "AGENTS.md", 3); got != filepath.Join(root, "AGENTS.md") {
		t.Fatalf("expected %q, got %q", filepath.Join(root, "AGENTS.md"), got)
	}
}

// TestFindUpward_ParentDir 向上命中
func TestFindUpward_ParentDir(t *testing.T) {
	disableUserHomeDir(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if got := findUpward(sub, "CLAUDE.md", 3); got != filepath.Join(root, "CLAUDE.md") {
		t.Fatalf("expected %q, got %q", filepath.Join(root, "CLAUDE.md"), got)
	}
}

// TestFindUpward_MaxLevels 超过最大级数不命中
func TestFindUpward_MaxLevels(t *testing.T) {
	disableUserHomeDir(t)
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	if err := os.MkdirAll(deep, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// root → a → b → c → d：向上 4 级才到 root，超过 3 级上限
	if got := findUpward(deep, "AGENTS.md", 3); got != "" {
		t.Fatalf("expected no match beyond 3 levels, got %q", got)
	}
}

// TestFindUpward_HomeBoundary 不越过用户主目录边界
func TestFindUpward_HomeBoundary(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	proj := filepath.Join(home, "proj", "sub")
	if err := os.MkdirAll(proj, 0755); err != nil {
		t.Fatal(err)
	}
	// 文件在 home 之外（root 下），home=root/home
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	old := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = old })

	if got := findUpward(proj, "AGENTS.md", 3); got != "" {
		t.Fatalf("expected no match across home boundary, got %q", got)
	}
}

// TestWithinOrEqual withinOrEqual 逻辑
func TestWithinOrEqual(t *testing.T) {
	if !withinOrEqual("/a/b/c", "/a/b") {
		t.Fatalf("expected /a/b/c within /a/b")
	}
	if !withinOrEqual("/a/b", "/a/b") {
		t.Fatalf("expected /a/b within itself")
	}
	if withinOrEqual("/a/bc", "/a/b") {
		t.Fatalf("expected /a/bc NOT within /a/b (prefix trap)")
	}
	if withinOrEqual("/a", "/a/b") {
		t.Fatalf("expected /a NOT within /a/b")
	}
}
