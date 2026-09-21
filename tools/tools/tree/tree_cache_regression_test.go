package tree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// newTruncatedTreeSession 构造一个包含"截断目录"（可见条目数 > MaxChildrenNum）的会话。
// 这种目录让 treeStates 的扫描不完整（treeStatesComplete == false），
// 即 P1-22 描述的 fingerprint == "" 场景。
func newTruncatedTreeSession(t *testing.T) *structs.Chats {
	t.Helper()
	testDir := t.TempDir()
	big := filepath.Join(testDir, "big")
	if err := os.Mkdir(big, 0755); err != nil {
		t.Fatal(err)
	}
	for i := range MaxChildrenNum + 1 {
		name := filepath.Join(big, fmt.Sprintf("f%03d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(testDir, "visible.txt"), []byte("v"), 0644); err != nil {
		t.Fatal(err)
	}
	return &structs.Chats{
		Root:                 testDir,
		CurrentActivatePath:  ".",
		EnableScopes:         map[string]bool{},
		TemporyDataOfRequest: make(map[string]any),
		TemporyDataOfSession: make(map[string]any),
	}
}

// resultSuccess 提取 writeTree 返回结果里的 success 字段。
func resultSuccess(resultMap map[string]*any) bool {
	if resultMap == nil {
		return false
	}
	if p, ok := resultMap["success"]; ok && p != nil {
		if b, ok := (*p).(bool); ok {
			return b
		}
	}
	return false
}

// resultError 提取 writeTree 返回结果里的 error 文本。
func resultError(resultMap map[string]*any) string {
	if resultMap == nil {
		return ""
	}
	if p, ok := resultMap["error"]; ok && p != nil {
		if msg, ok := (*p).(string); ok {
			return msg
		}
	}
	return ""
}

// assertIncompleteScan 确认当前工作区确实触发了不完整扫描，避免用例失去复现意义。
func assertIncompleteScan(t *testing.T, root string) {
	t.Helper()
	states, err := treeStates(root)
	if err != nil {
		t.Fatalf("treeStates: %v", err)
	}
	if treeStatesComplete(states) {
		t.Fatalf("test setup: expected an incomplete scan")
	}
}

// TestBuildGlobalPrompt_IncompleteScanNoStaleSnapshot 回归 P1-22（陈旧快照部分）：
// 扫描不完整时 fingerprint 在修复前恒为空，缓存既无法增量刷新也触发不了重建，
// 目录形状变化后仍持续返回旧快照。
func TestBuildGlobalPrompt_IncompleteScanNoStaleSnapshot(t *testing.T) {
	session := newTruncatedTreeSession(t)
	assertIncompleteScan(t, session.Root)

	if _, err := buildGlobalPrompt(session); err != nil {
		t.Fatalf("first buildGlobalPrompt: %v", err)
	}
	first, ok := session.TemporyDataOfRequest[treeCacheKey].(*cacheStruct)
	if !ok || first == nil {
		t.Fatal("expected request tree cache")
	}
	if !strings.Contains(first.TreeString, "visible.txt") {
		t.Fatalf("test setup: first snapshot should contain visible.txt, got:\n%s", first.TreeString)
	}

	// 目录形状发生变化（新增根级文件），截断目录仍让扫描不完整。
	if err := os.WriteFile(filepath.Join(session.Root, "created.txt"), []byte("n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 模拟下一次请求：request 级缓存清空，session 级缓存保留。
	session.TemporyDataOfRequest = make(map[string]any)
	prompt, err := buildGlobalPrompt(session)
	if err != nil {
		t.Fatalf("second buildGlobalPrompt: %v", err)
	}
	if !strings.Contains(prompt, "created.txt") {
		t.Fatalf("incomplete scan served a stale snapshot: created.txt missing from prompt")
	}
}

// TestWriteTree_IncompleteScanCreateNotRejected 回归 P1-22（编辑恒被拒绝部分）：
// 修复前不完整扫描存入空 Fingerprint，writeTree 用非空指纹比对必然不等，
// 目录未变化时也会被 "Tree changed during edit" 拒绝。
func TestWriteTree_IncompleteScanCreateNotRejected(t *testing.T) {
	session := newTruncatedTreeSession(t)
	assertIncompleteScan(t, session.Root)

	if _, err := buildGlobalPrompt(session); err != nil {
		t.Fatal(err)
	}
	cached, ok := session.TemporyDataOfRequest[treeCacheKey].(*cacheStruct)
	if !ok || cached == nil {
		t.Fatal("expected request tree cache")
	}

	// 目录没有任何变化，只向树文本追加一个新文件。
	mp := map[string]*any{
		"path":   strPtr("@tree"),
		"target": strPtr("@all"),
		"text":   strPtr(cached.TreeString + "\n    - newfile.txt `999`"),
	}
	_, _, resultMap, err := writeTree(session, mp, []*any{})
	if err != nil {
		t.Fatalf("writeTree: %v", err)
	}
	if !resultSuccess(resultMap) {
		t.Fatalf("unchanged incomplete tree must not be rejected, error=%q", resultError(resultMap))
	}
	if _, statErr := os.Stat(filepath.Join(session.Root, "newfile.txt")); statErr != nil {
		t.Fatalf("newfile.txt should be created: %v", statErr)
	}
}

// TestWriteTree_IncompleteScanDeleteNotRejected 回归：同一条写回路径上的删除操作
// 在不完整扫描缓存下也必须正常工作（不能因指纹为空恒被拒绝）。
func TestWriteTree_IncompleteScanDeleteNotRejected(t *testing.T) {
	session := newTruncatedTreeSession(t)
	assertIncompleteScan(t, session.Root)

	if _, err := buildGlobalPrompt(session); err != nil {
		t.Fatal(err)
	}
	cached, ok := session.TemporyDataOfRequest[treeCacheKey].(*cacheStruct)
	if !ok || cached == nil {
		t.Fatal("expected request tree cache")
	}

	lines := strings.Split(cached.TreeString, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.Contains(ln, "visible.txt") {
			continue
		}
		kept = append(kept, ln)
	}
	if len(kept) == len(lines) {
		t.Fatalf("test setup: visible.txt not found in tree:\n%s", cached.TreeString)
	}

	mp := map[string]*any{
		"path":   strPtr("@tree"),
		"target": strPtr("@all"),
		"text":   strPtr(strings.Join(kept, "\n")),
	}
	_, _, resultMap, err := writeTree(session, mp, []*any{})
	if err != nil {
		t.Fatalf("writeTree: %v", err)
	}
	if !resultSuccess(resultMap) {
		t.Fatalf("delete on unchanged incomplete tree must not be rejected, error=%q", resultError(resultMap))
	}
	if _, statErr := os.Stat(filepath.Join(session.Root, "visible.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("visible.txt should be deleted, stat err=%v", statErr)
	}
}

// TestWriteTree_IncompleteScanWithoutCacheNotRejected 回归：会话里还没有任何 tree 缓存时，
// writeTree 必须先自行重建快照；不完整扫描建出的快照同样不能恒因指纹为空被拒绝。
func TestWriteTree_IncompleteScanWithoutCacheNotRejected(t *testing.T) {
	session := newTruncatedTreeSession(t)
	assertIncompleteScan(t, session.Root)

	// 不调用 buildGlobalPrompt，模拟缓存不可用（新会话/缓存被清理）的写回路径。
	if len(session.TemporyDataOfSession) != 0 || len(session.TemporyDataOfRequest) != 0 {
		t.Fatal("test setup: caches should be empty")
	}
	treeID := int32(0)
	node, _ := BuildTree(session.Root, &treeID, 0)
	node.Name = "(root)"
	treeStr := BuildString(node)

	mp := map[string]*any{
		"path":   strPtr("@tree"),
		"target": strPtr("@all"),
		"text":   strPtr(treeStr + "\n    - fresh.txt `999`"),
	}
	_, _, resultMap, err := writeTree(session, mp, []*any{})
	if err != nil {
		t.Fatalf("writeTree: %v", err)
	}
	if !resultSuccess(resultMap) {
		t.Fatalf("edit without cache on incomplete tree must not be rejected, error=%q", resultError(resultMap))
	}
	if _, statErr := os.Stat(filepath.Join(session.Root, "fresh.txt")); statErr != nil {
		t.Fatalf("fresh.txt should be created: %v", statErr)
	}
}
