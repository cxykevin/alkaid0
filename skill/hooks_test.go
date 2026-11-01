package skill

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// TestActivateDeactivateHooks 验证 Activate/Deactivate 会通知上层钩子
// （trace 包据此维护读取上下文）。
func TestActivateDeactivateHooks(t *testing.T) {
	requireSymlink(t)
	ws := t.TempDir()
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "demo", "---\nname: demo\n---\n")
	sk, ok := Lookup(ws, "skill.demo")
	if !ok {
		t.Fatal("未发现 skill.demo")
	}

	var enabled, disabled string
	SetHooks(Hooks{
		OnEnabled:  func(session *structs.Chats, s Skill) { enabled = s.Name },
		OnDisabled: func(session *structs.Chats, name string) { disabled = name },
	})
	t.Cleanup(func() { SetHooks(Hooks{}) })

	session := &structs.Chats{Root: ws}
	if err := Activate(session, sk); err != nil {
		t.Fatalf("Activate failed: %v", err)
	}
	if enabled != "demo" {
		t.Errorf("OnEnabled 未触发或名称错误: %q", enabled)
	}
	if err := Deactivate(session, "demo"); err != nil {
		t.Fatalf("Deactivate failed: %v", err)
	}
	if disabled != "demo" {
		t.Errorf("OnDisabled 未触发或名称错误: %q", disabled)
	}
}

// TestActivateHookNotFiredOnFailure 验证链接建立失败时不会触发 OnEnabled
// （避免"没有文件却写入了读取上下文"）。
func TestActivateHookNotFiredOnFailure(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "demo", "---\nname: demo\n---\n")
	sk, ok := Lookup(ws, "skill.demo")
	if !ok {
		t.Fatal("未发现 skill.demo")
	}
	// 链接位置放一个真实目录：Enable 会拒绝，Activate 应直接返回错误。
	if err := os.MkdirAll(filepath.Join(ws, ".alkaid0", "skills", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}

	fired := false
	SetHooks(Hooks{OnEnabled: func(*structs.Chats, Skill) { fired = true }})
	t.Cleanup(func() { SetHooks(Hooks{}) })

	session := &structs.Chats{Root: ws}
	if err := Activate(session, sk); err == nil {
		t.Fatal("同名真实目录存在时 Activate 应报错")
	}
	if fired {
		t.Error("链接失败时不应触发 OnEnabled")
	}
}
