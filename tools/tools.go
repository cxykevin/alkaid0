package tools

import (
	"errors"

	"github.com/cxykevin/alkaid0/log"
)

// ToolsList 工具列表
var ToolsList map[string]*Tools

// Scopes 工具命名空间
var Scopes map[string]string

// 启用的命名空间
var enableScopes map[string]bool

var logger *log.LogsObj

func init() {
	logger = log.New("tools")
}

// AddScope 添加工具命名空间
func AddScope(name string, prompt string) {
	Scopes[name] = prompt
}

// AddTool 添加工具
func AddTool(tool *Tools) {
	ToolsList[tool.ID] = tool
}

// HookTool 为工具添加钩子
func HookTool(name string, hook *Hook) {
	ToolsList[name].Hooks = append(ToolsList[name].Hooks, *hook)
}

// ExecToolGetPrompts 执行预调用，获取提示词表
func ExecToolGetPrompts(name string) ([]string, []string) {
	// 执行 PreHook
	unusedHooks := make([]string, 0)
	for name, prompts := range Scopes {
		if val, ok := enableScopes[name]; !ok || !val {
			unusedHooks = append(unusedHooks, prompts)
		}
	}

	prehooks := make([]string, 0)
	for _, hook := range ToolsList[name].Hooks {
		if _, ok := Scopes[hook.Scope]; !ok {
			logger.Error("hook scope \"%v\" not found", hook.Scope)
			continue
		}
		if val, ok := enableScopes[hook.Scope]; !ok || !val {
			continue
		}
		ret, err := hook.PreHook()
		if err != nil {
			logger.Error("hook pre hook error: %v", err)
			continue
		}
		prehooks = append(prehooks, ret)
	}
	return unusedHooks, prehooks
}

// ExecToolOnHook 执行工具
func ExecToolOnHook(name string, args map[string]any) []any {
	onhooks := make([]any, 0)
	pass := make([]*any, 0)
	for _, hook := range ToolsList[name].Hooks {
		if _, ok := Scopes[hook.Scope]; !ok {
			continue
		}
		if val, ok := enableScopes[hook.Scope]; !ok || !val {
			continue
		}
		ret, passFunc, err := hook.OnHook(args, pass)
		pass = passFunc
		if err != nil {
			logger.Error("hook on hook error: %v", err)
			continue
		}
		onhooks = append(onhooks, ret)
	}
	return onhooks
}

// ExecToolPostHook 执行工具
func ExecToolPostHook(name string, args map[string]any) (map[string]any, error) {
	passObjs := make([]*any, 0)
	for _, hook := range ToolsList[name].Hooks {
		if _, ok := Scopes[hook.Scope]; !ok {
			continue
		}
		if val, ok := enableScopes[hook.Scope]; !ok || !val {
			continue
		}
		pass, passObj, ret, err := hook.PostHook(args, passObjs)
		passObjs = passObj
		if err != nil {
			logger.Error("hook post hook error: %v", err)
			return map[string]any{}, err
		}
		if pass {
			continue
		}
		return ret, nil
	}
	logger.Error("all tool passed")
	return map[string]any{}, errors.New("All tool passed")
}

// EnableScope 启用命名空间
func EnableScope(scope string) {
	enableScopes[scope] = true
}

// DisableScope 禁用命名空间
func DisableScope(scope string) {
	enableScopes[scope] = false
}
