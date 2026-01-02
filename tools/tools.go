package tools

import (
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
	toolobj.EnableScopes[""] = true
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
		if hook.Parameters != nil {
			maps.Copy(paras, *hook.Parameters)
		}
	}
	return unusedHooks, prehooks, paras
}

// ExecToolOnHook 执行工具
func ExecToolOnHook(name string, args map[string]*any) error {
	passObjs := make([]*any, 0)

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
		pass, passObj, err := hook.OnHook.Func(args, passObjs)
		passObjs = passObj
		if err != nil {
			logger.Error("hook post hook error: %v", err)
			return err
		}
		if pass {
			continue
		}
		return nil
	}
	// logger.Error("all tool passed")
	return nil
}

// ExecToolPostHook 执行工具
func ExecToolPostHook(name string, args map[string]*any) (map[string]*any, error) {
	passObjs := make([]*any, 0)
	hookTmp := toolobj.ToolsList[name].Hooks

	// 将tmp中的钩子按Priority排序
	sort.Slice(hookTmp, func(i, j int) bool {
		return hookTmp[i].PostHook.Priority > hookTmp[j].PostHook.Priority
	})

	for _, hook := range hookTmp {
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
			return map[string]*any{}, err
		}
		if pass {
			continue
		}
		return ret, nil
	}
	// logger.Error("all tool passed")
	return map[string]*any{}, nil
}
