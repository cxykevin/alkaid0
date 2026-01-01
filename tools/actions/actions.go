package actions

import (
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// AddScope 添加工具命名空间
func AddScope(name string, prompt string) {
	toolobj.Scopes[name] = prompt
}

// AddTool 添加工具
func AddTool(tool *toolobj.Tools) {
	toolobj.ToolsList[tool.ID] = tool
}

// HookTool 为工具添加钩子
func HookTool(name string, hook *toolobj.Hook) {
	toolobj.ToolsList[name].Hooks = append(toolobj.ToolsList[name].Hooks, *hook)
}

// EnableScope 启用命名空间
func EnableScope(scope string) {
	if scope == "" {
		return
	}
	toolobj.EnableScopes[scope] = true
	if err := SetScopeEnabled(scope, true); err != nil {
		logger.Error("failed to persist enable scope %s: %v", scope, err)
	}
}

// DisableScope 禁用命名空间
func DisableScope(scope string) {
	if scope == "" {
		return
	}
	toolobj.EnableScopes[scope] = false
	if err := SetScopeEnabled(scope, false); err != nil {
		logger.Error("failed to persist disable scope %s: %v", scope, err)
	}
}
