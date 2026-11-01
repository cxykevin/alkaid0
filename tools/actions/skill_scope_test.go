package actions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/internal/skillpath"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// newSkillWorkspace 构造一个含 skill.demo 的工作区。
func newSkillWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, ".claude", "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillpath.ManifestName), []byte("---\nname: demo\ndescription: demo skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

// requireSymlinkSupport 在平台不支持符号链接时跳过用例（Windows 非开发者模式）。
func requireSymlinkSupport(t *testing.T) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe-target")
	if err := os.MkdirAll(probe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(probe, filepath.Join(t.TempDir(), "probe-link")); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}
}

// TestEnableDisableSkillScope 验证 skill scope 的启用/停用：链接落位、状态持久化。
//
// trace list 的注入/移除由 skill 钩子完成（trace 包注册，见 trace 包测试），
// 这里只验证 actions 层的链接与数据库状态。
func TestEnableDisableSkillScope(t *testing.T) {
	ws := newSkillWorkspace(t)
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	requireSymlinkSupport(t)

	session := &structs.Chats{
		ID:           2,
		Root:         ws,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}

	if err := EnableScope(session, "skill.demo"); err != nil {
		t.Fatalf("EnableScope(skill.demo) failed: %v", err)
	}
	link := filepath.Join(skillpath.LinkDir(ws), "demo")
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("启用后应存在符号链接: %v", err)
	}
	if !session.EnableScopes["skill.demo"] {
		t.Error("session.EnableScopes 未记录 skill.demo")
	}
	var row structs.Scopes
	if err := db.Where("name = ? AND chat_id = ?", "skill.demo", session.ID).First(&row).Error; err != nil {
		t.Fatalf("数据库中没有 skill.demo: %v", err)
	}
	if !row.Enabled {
		t.Error("数据库中的 skill.demo 应为启用状态")
	}

	if err := DisableScope(session, "skill.demo"); err != nil {
		t.Fatalf("DisableScope(skill.demo) failed: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("停用后链接应被删除: %v", err)
	}
	if session.EnableScopes["skill.demo"] {
		t.Error("session.EnableScopes 应记录 skill.demo 为停用")
	}
	if err := db.Where("name = ? AND chat_id = ?", "skill.demo", session.ID).First(&row).Error; err != nil {
		t.Fatalf("数据库中没有 skill.demo: %v", err)
	}
	if row.Enabled {
		t.Error("数据库中的 skill.demo 应为停用状态")
	}
	// 真实技能目录保留
	if _, err := os.Stat(filepath.Join(ws, ".claude", "skills", "demo", skillpath.ManifestName)); err != nil {
		t.Errorf("停用不应删除技能目录: %v", err)
	}
}

// TestEnableScopeUnknownSkill 验证不存在的 skill 会报错且不落状态。
func TestEnableScopeUnknownSkill(t *testing.T) {
	ws := newSkillWorkspace(t)
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	session := &structs.Chats{
		ID:           3,
		Root:         ws,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}
	if err := EnableScope(session, "skill.missing"); err == nil {
		t.Fatal("不存在的 skill 应返回错误")
	}
	if session.EnableScopes["skill.missing"] {
		t.Error("失败时不应修改 session 状态")
	}
	var count int64
	if err := db.Model(&structs.Scopes{}).Where("chat_id = ?", session.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("失败时不应写入 scopes 表，得到 %d 行", count)
	}
}

// TestSyncEnabledOnLoad 验证会话加载时已启用的 skill 链接会补齐。
func TestSyncEnabledOnLoad(t *testing.T) {
	ws := newSkillWorkspace(t)
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	requireSymlinkSupport(t)

	session := &structs.Chats{ID: 4, Root: ws, DB: db, EnableScopes: make(map[string]bool)}
	if err := EnableScope(session, "skill.demo"); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(skillpath.LinkDir(ws), "demo")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}

	// 模拟冷加载：从数据库恢复启用状态并补链接
	loaded := &structs.Chats{ID: 4, Root: ws, DB: db, EnableScopes: make(map[string]bool)}
	Load(loaded)
	if !loaded.EnableScopes["skill.demo"] {
		t.Fatal("Load 未从数据库恢复 skill.demo")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("Load 应补齐已启用 skill 的链接: %v", err)
	}
}
