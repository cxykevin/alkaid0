package skill

import (
	"errors"
	"sync"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// Hooks skill 生命周期钩子，由上层（tools/tools/trace）注册。
//
// skill 包被 trace 包依赖（trace → skill），因此这里用注册表解耦：启用 skill 时
// 由 trace 把 SKILL.md 加入会话读取上下文（trace list），停用时移除该 skill 目录
// 下的全部 trace。skill 自身不直接引用 trace，避免循环导入。
type Hooks struct {
	// OnEnabled 启用 skill 后调用：把 SKILL.md 加入读取上下文。
	OnEnabled func(session *structs.Chats, s Skill)
	// OnDisabled 停用 skill 后调用：移除该 skill 目录下的所有 trace。
	OnDisabled func(session *structs.Chats, name string)
}

var (
	hooksMu sync.RWMutex
	hooks   Hooks
)

// SetHooks 注册 skill 生命周期钩子；传零值可清除。
func SetHooks(h Hooks) {
	hooksMu.Lock()
	hooks = h
	hooksMu.Unlock()
}

// Activate 在指定会话中启用 skill：建立 .alkaid0/skills/<name> 链接，
// 并通知上层把 SKILL.md 加入读取上下文（trace list）。
func Activate(session *structs.Chats, s Skill) error {
	if session == nil {
		return errors.New("session is nil")
	}
	if err := Enable(session.Root, s); err != nil {
		return err
	}
	hooksMu.RLock()
	onEnabled := hooks.OnEnabled
	hooksMu.RUnlock()
	if onEnabled != nil {
		onEnabled(session, s)
	}
	return nil
}

// Deactivate 在指定会话中停用 skill：删除链接，并通知上层移除该 skill 路径下的
// 所有 trace（无论链接删除是否成功——scope 已经停用，上下文里不应再保留技能内容）。
func Deactivate(session *structs.Chats, name string) error {
	if session == nil {
		return errors.New("session is nil")
	}
	err := Disable(session.Root, name)
	hooksMu.RLock()
	onDisabled := hooks.OnDisabled
	hooksMu.RUnlock()
	if onDisabled != nil {
		onDisabled(session, name)
	}
	return err
}
