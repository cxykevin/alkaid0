package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/internal/skillpath"
	"github.com/cxykevin/alkaid0/skill"
	"github.com/cxykevin/alkaid0/storage/structs"
)

func requireSymlinkT(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}
}

// newSkillSession 构造带 skill.demo 的会话（含数据库）。
func newSkillSession(t *testing.T) (*structs.Chats, skill.Skill) {
	t.Helper()
	db := setupTestDB(t)
	ws := t.TempDir()
	dir := filepath.Join(ws, ".claude", "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "---\nname: demo\ndescription: demo skill\n---\n# Demo\n\nDemo body.\n"
	if err := os.WriteFile(filepath.Join(dir, skillpath.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	sk, ok := skill.Lookup(ws, "skill.demo")
	if !ok {
		t.Fatal("未发现 skill.demo")
	}
	session := &structs.Chats{ID: 42, Root: ws, DB: db, EnableScopes: map[string]bool{}}
	return session, sk
}

func countTraces(t *testing.T, session *structs.Chats, pathLike string) int64 {
	t.Helper()
	var count int64
	if err := session.DB.Model(&structs.Traces{}).
		Where("chat_id = ? AND path LIKE ?", session.ID, pathLike).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// TestSkillActivateAddsManifestTrace 验证启用 skill 会把 SKILL.md 加入 trace list。
func TestSkillActivateAddsManifestTrace(t *testing.T) {
	requireSymlinkT(t)
	session, sk := newSkillSession(t)

	if err := skill.Activate(session, sk); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	var row structs.Traces
	if err := session.DB.
		Where("chat_id = ? AND path = ?", session.ID, sk.WorkspaceManifest()).
		First(&row).Error; err != nil {
		t.Fatalf("SKILL.md 未加入 trace list: %v", err)
	}
	if !strings.Contains(row.LastContent, "Demo body.") {
		t.Errorf("trace 内容 = %q，期望包含 SKILL.md 正文", row.LastContent)
	}
	if row.ReadMode != "" {
		t.Errorf("SKILL.md 应按全文注入，ReadMode = %q", row.ReadMode)
	}

	// 重复启用幂等：不会重复插入
	if err := skill.Activate(session, sk); err != nil {
		t.Fatalf("重复 Activate 失败: %v", err)
	}
	if n := countTraces(t, session, "%"+sk.WorkspaceManifest()); n != 1 {
		t.Errorf("重复启用后 trace 行数 = %d, want 1", n)
	}
}

// TestSkillDeactivateRemovesTraces 验证停用会移除该 skill 路径下的所有 trace，
// 且不影响其它路径（包括名字前缀相同的 skill）。
func TestSkillDeactivateRemovesTraces(t *testing.T) {
	requireSymlinkT(t)
	session, sk := newSkillSession(t)
	if err := skill.Activate(session, sk); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	extra := []structs.Traces{
		// 同一 skill 目录下、其它 Agent 的 trace
		{Path: sk.WorkspaceManifest(), ChatID: session.ID, AgentID: "sub", TraceID: 99, LastContent: "sub"},
		// 同一目录下的其它文件
		{Path: skill.WorkspaceDir("demo") + "/helper.md", ChatID: session.ID, AgentID: "", TraceID: 100},
		// 名字前缀相同但不是同一个 skill
		{Path: skill.WorkspaceManifest("demo2"), ChatID: session.ID, AgentID: "", TraceID: 101},
		// 普通文件
		{Path: "main.go", ChatID: session.ID, AgentID: "", TraceID: 102},
	}
	for i := range extra {
		if err := session.DB.Create(&extra[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := skill.Deactivate(session, "demo"); err != nil {
		t.Fatalf("Deactivate failed: %v", err)
	}

	if n := countTraces(t, session, "%"+skill.WorkspaceDir("demo")+"/%"); n != 0 {
		t.Errorf("skill 目录下的 trace 应全部删除，剩余 %d 行", n)
	}
	if n := countTraces(t, session, skill.WorkspaceManifest("demo2")); n != 1 {
		t.Errorf("名字前缀相同的其它 skill 不应被删除（剩余 %d 行）", n)
	}
	if n := countTraces(t, session, "main.go"); n != 1 {
		t.Errorf("无关文件的 trace 不应被删除（剩余 %d 行）", n)
	}
}

// readToolResult 调用 read 工具并返回结果表。
func readToolResult(t *testing.T, session *structs.Chats, path string) map[string]*any {
	t.Helper()
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	pathVal := any(path)
	_, _, result, err := Trace(session, map[string]*any{"path": &pathVal}, []*any{})
	if err != nil {
		t.Fatalf("read %q 返回错误: %v", path, err)
	}
	if result == nil {
		t.Fatalf("read %q 未返回结果", path)
	}
	return result
}

func resultSuccess(t *testing.T, result map[string]*any) bool {
	t.Helper()
	ptr, ok := result["success"]
	if !ok || ptr == nil {
		return false
	}
	success, ok := (*ptr).(bool)
	return ok && success
}

// TestReadSkillLinkedPath 验证 read 能读已启用 skill 的链接内容，
// 而 .alkaid0 下的其它文件仍然不可读。
func TestReadSkillLinkedPath(t *testing.T) {
	requireSymlinkT(t)
	session, sk := newSkillSession(t)
	if err := skill.Activate(session, sk); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	path := sk.WorkspaceManifest()
	if result := readToolResult(t, session, path); !resultSuccess(t, result) {
		t.Fatalf("read %q 应成功，实际结果: %v", path, result)
	}

	// .alkaid0 内的普通文件（非 skill 链接）继续受保护
	secret := filepath.Join(session.Root, skillpath.WorkspaceDataDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel := skillpath.WorkspaceDataDir + "/secret.txt"
	if result := readToolResult(t, session, rel); resultSuccess(t, result) {
		t.Errorf("read %q 应被拒绝，但它成功了", rel)
	}
	if n := countTraces(t, session, rel); n != 0 {
		t.Errorf(".alkaid0 下的普通文件不应进入 trace list（剩余 %d 行）", n)
	}
}

// TestRenderInjectsSkillContent 验证已启用的 skill 内容会进入上下文，
// 即使事件表非空且没有该 skill 的 read 事件（启用是自动注入，不是工具调用）。
func TestRenderInjectsSkillContent(t *testing.T) {
	requireSymlinkT(t)
	session, sk := newSkillSession(t)
	if err := skill.Activate(session, sk); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	// 模拟生产链路：事件表非空，但只有普通文件的 read 事件（skill 没有工具调用事件）
	session.TemporyDataOfSession = map[string]any{
		structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
			"main.go": {MsgID: 7},
		},
	}

	topBlock, _, err := RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks failed: %v", err)
	}
	if !strings.Contains(topBlock, "Demo body.") {
		t.Errorf("skill 内容未注入上下文：\n%s", topBlock)
	}
	if !strings.Contains(topBlock, sk.WorkspaceManifest()) {
		t.Errorf("注入内容缺少 skill 路径 %q：\n%s", sk.WorkspaceManifest(), topBlock)
	}
}
