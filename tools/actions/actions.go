package actions

import (
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/skill"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// AddScope 添加工具命名空间
func AddScope(name string, prompt string) {
	toolobj.SetScope(name, prompt)
}

// AddTool 添加工具
func AddTool(tool *toolobj.Tools) {
	toolobj.SetTool(tool)
}

// SearchTool 找工具
func SearchTool(name string, scope string) *toolobj.Tools {
	return toolobj.SearchTool(name, scope)
}

// HookTool 为工具添加钩子
func HookTool(name string, hook *toolobj.Hook) error {
	if ok := toolobj.AppendToolHook(name, *hook); !ok {
		return fmt.Errorf("tool %q not registered before HookTool", name)
	}
	return nil
}

// EnableScope 启用命名空间
func EnableScope(session *structs.Chats, scope string) error {
	if scope == "" {
		return nil
	}
	// skill scope 不注册在全局 Scopes 里（按工作区动态发现），单独校验与加载。
	if skill.IsSkillScope(scope) {
		sk, ok := skill.Lookup(session.Root, scope)
		if !ok {
			return fmt.Errorf("scope \"%v\" not found", scope)
		}
		// 先落链接再改状态：链接失败时保持 scope 原状，不产生"已启用但没有文件"的会话。
		// Activate 还会把 SKILL.md 加入会话读取上下文（trace list）。
		if err := skill.Activate(session, sk); err != nil {
			return err
		}
		session.EnableScopes[scope] = true
		if err := SetScopeEnabled(session.DB, session.ID, scope, true); err != nil {
			logger.Error("failed to persist enable scope %s: %v", scope, err)
			return err
		}
		return nil
	}
	_, ok := toolobj.GetScope(scope)
	if !ok {
		return fmt.Errorf("scope \"%v\" not found", scope)
	}
	session.EnableScopes[scope] = true
	if err := SetScopeEnabled(session.DB, session.ID, scope, true); err != nil {
		logger.Error("failed to persist enable scope %s: %v", scope, err)
		return err
	}
	return nil
}

// DisableScope 禁用命名空间
func DisableScope(session *structs.Chats, scope string) error {
	if scope == "" {
		return nil
	}
	if skill.IsSkillScope(scope) {
		// 链接删除失败（例如同名真实目录）只记日志：状态仍要能停用，
		// 但绝不删除非软链接内容。
		// Deactivate 同时移除该 skill 路径下的所有 trace（读取上下文）。
		if err := skill.Deactivate(session, strings.TrimPrefix(scope, skill.ScopePrefix)); err != nil {
			logger.Warn("remove skill link for %s failed: %v", scope, err)
		}
		session.EnableScopes[scope] = false
		if err := SetScopeEnabled(session.DB, session.ID, scope, false); err != nil {
			logger.Error("failed to persist disable scope %s: %v", scope, err)
			return err
		}
		return nil
	}
	_, ok := toolobj.GetScope(scope)
	if !ok {
		return fmt.Errorf("scope \"%v\" not found", scope)
	}
	session.EnableScopes[scope] = false
	if err := SetScopeEnabled(session.DB, session.ID, scope, false); err != nil {
		logger.Error("failed to persist disable scope %s: %v", scope, err)
		return err
	}
	return nil
}
