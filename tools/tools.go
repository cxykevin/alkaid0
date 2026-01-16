package tools

import (
	"maps"
	"sort"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
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
}

func checkScopeEnabled(session *structs.Chats, scope string) bool {
	if scope == "" {
		return true
	}
	if v, ok := session.EnableScopes[scope]; !ok || !v {
		return false
	}
	return true
}

// ExecOneToolGetPrompts 执行预调用，获取提示词表
func ExecOneToolGetPrompts(session *structs.Chats, name string) ([]string, []string, map[string]parser.ToolParameters) {
	// 执行 PreHook
	unusedHooks := make([]string, 0)
	for name, prompts := range toolobj.Scopes {
		if !checkScopeEnabled(session, name) {
			unusedHooks = append(unusedHooks, prompts)
		}
	}

	prehooks := make([]string, 0)
	// 检查工具是否存在
	if toolobj.ToolsList[name] == nil {
		return unusedHooks, prehooks, make(map[string]parser.ToolParameters)
	}

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
		if !checkScopeEnabled(session, hook.Scope) {
			continue
		}
		ret, err := hook.PreHook.Func(session)
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
func ExecToolOnHook(session *structs.Chats, name string, args map[string]*any) error {
	passObjs := make([]*any, 0)

	// 检查工具是否存在
	if toolobj.ToolsList[name] == nil {
		return nil
	}

	hookTmp := toolobj.ToolsList[name].Hooks

	// 将tmp中的钩子按Priority排序
	sort.Slice(hookTmp, func(i, j int) bool {
		return hookTmp[i].OnHook.Priority > hookTmp[j].OnHook.Priority
	})

	for _, hook := range hookTmp {
		if _, ok := toolobj.Scopes[hook.Scope]; !ok {
			continue
		}
		if !checkScopeEnabled(session, hook.Scope) {
			continue
		}
		pass, passObj, err := hook.OnHook.Func(session, args, passObjs)
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
func ExecToolPostHook(session *structs.Chats, name string, args map[string]*any) (map[string]*any, error) {
	passObjs := make([]*any, 0)

	// 检查工具是否存在
	if toolobj.ToolsList[name] == nil {
		return map[string]*any{}, nil
	}

	hookTmp := toolobj.ToolsList[name].Hooks

	// 将tmp中的钩子按Priority排序
	sort.Slice(hookTmp, func(i, j int) bool {
		return hookTmp[i].PostHook.Priority > hookTmp[j].PostHook.Priority
	})

	for _, hook := range hookTmp {
		if _, ok := toolobj.Scopes[hook.Scope]; !ok {
			continue
		}
		if !checkScopeEnabled(session, hook.Scope) {
			continue
		}
		pass, passObj, ret, err := hook.PostHook.Func(session, args, passObjs)
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
