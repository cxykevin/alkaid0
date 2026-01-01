package tools

import (
	"errors"
	"maps"
	"sort"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

var logger *log.LogsObj

func init() {
	logger = log.New("tools")
	toolobj.Scopes[""] = "Global"
	toolobj.ToolsList[""] = &toolobj.Tools{
		Name: "Global",
		ID:   "",
	}
	// 尝试从数据库加载命名空间启用状态（若 DB 未初始化则忽略）
	if scs, err := GetAllScopes(); err == nil {
		maps.Copy(toolobj.EnableScopes, scs)
	} else {
		logger.Error("failed to load scopes from storage: %v", err)
	}
	toolobj.EnableScopes[""] = true
}

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

// ExecOneToolGetPrompts 执行预调用，获取提示词表
func ExecOneToolGetPrompts(name string) ([]string, []string, map[string]parser.ToolParameters) {
	// 执行 PreHook
	unusedHooks := make([]string, 0)
	for name, prompts := range toolobj.Scopes {
		if val, ok := toolobj.EnableScopes[name]; !ok || !val {
			unusedHooks = append(unusedHooks, prompts)
		}
	}

	prehooks := make([]string, 0)
	hookTmp := toolobj.ToolsList[name].Hooks
	paras := toolobj.ToolsList[name].Parameters

	// 将tmp中的钩子按Priority排序
	sort.Slice(hookTmp, func(i, j int) bool {
		return hookTmp[i].PreHook.Priority > hookTmp[j].PreHook.Priority
	})

	for _, hook := range hookTmp {
		if _, ok := toolobj.Scopes[hook.Scope]; !ok {
			logger.Error("hook scope \"%v\" not found", hook.Scope)
			continue
		}
		if val, ok := toolobj.EnableScopes[hook.Scope]; !ok || !val {
			continue
		}
		ret, err := hook.PreHook.Func()
		if err != nil {
			logger.Error("hook pre hook error: %v", err)
			continue
		}
		prehooks = append(prehooks, ret)
		// 合并map
		maps.Copy(paras, hook.Parameters)
	}
	return unusedHooks, prehooks, paras
}

// ExecToolOnHook 执行工具
func ExecToolOnHook(name string, args map[string]any) []any {
	onhooks := make([]any, 0)
	pass := make([]*any, 0)
	hookTmp := toolobj.ToolsList[name].Hooks

	// 将tmp中的钩子按Priority排序
	sort.Slice(hookTmp, func(i, j int) bool {
		return hookTmp[i].OnHook.Priority > hookTmp[j].OnHook.Priority
	})

	for _, hook := range hookTmp {
		if _, ok := toolobj.Scopes[hook.Scope]; !ok {
			continue
		}
		if val, ok := toolobj.EnableScopes[hook.Scope]; !ok || !val {
			continue
		}
		ret, passFunc, err := hook.OnHook.Func(args, pass)
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
	for _, hook := range toolobj.ToolsList[name].Hooks {
		if _, ok := toolobj.Scopes[hook.Scope]; !ok {
			continue
		}
		if val, ok := toolobj.EnableScopes[hook.Scope]; !ok || !val {
			continue
		}
		pass, passObj, ret, err := hook.PostHook.Func(args, passObjs)
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
