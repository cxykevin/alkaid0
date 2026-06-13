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
// 返回三部分：未启用 scope 的提示词、已启用 PreHook 的返回文本、工具参数定义
// PreHook 按 Priority 降序执行（高优先级先执行），参数定义逐层合并
func ExecOneToolGetPrompts(session *structs.Chats, name string) ([]string, []string, map[string]parser.ToolParameters) {
	logger.Debug("getting prompts for tool: %s", name)
	// 收集所有未启用的 scope 的提示词，用于告知 AI 哪些工具当前不可用
	unusedHooks := make([]string, 0)
	for name, prompts := range toolobj.Scopes {
		if !checkScopeEnabled(session, name) {
			unusedHooks = append(unusedHooks, prompts)
		}
	}

	prehooks := make([]string, 0)
	// 检查工具是否存在
	if val, ok := toolobj.ToolsList[name]; !ok || val == nil {
		return unusedHooks, prehooks, make(map[string]parser.ToolParameters)
	}

	hookTmp := toolobj.ToolsList[name].Hooks
	paras := toolobj.ToolsList[name].Parameters

	// 将tmp中的钩子按Priority排序（高优先级排在前面，先执行）
	sort.Slice(hookTmp, func(i, j int) bool {
		return hookTmp[i].PreHook.Priority > hookTmp[j].PreHook.Priority
	})

	// 遍历所有 hook，仅执行已启用 scope 的 PreHook
	// 执行结果文本追加到 prehooks 列表，最终合并为工具上下文的提示词
	for _, hook := range hookTmp {
		if _, ok := toolobj.Scopes[hook.Scope]; !ok {
			logger.Error("hook scope \"%v\" not found", hook.Scope)
			continue
		}
		if !checkScopeEnabled(session, hook.Scope) {
			continue
		}
		if hook.PreHook.Func != nil {
			ret, err := hook.PreHook.Func(session)
			if err != nil {
				logger.Error("hook pre hook error: %v", err)
				continue
			}
			prehooks = append(prehooks, ret)
		}
		// 合并map
		if hook.Parameters != nil {
			maps.Copy(paras, *hook.Parameters)
		}
	}
	return unusedHooks, prehooks, paras
}

// ExecToolOnHook 执行工具的 OnHook 回调。
// 按 Priority 降序执行所有已启用 scope 的 OnHook 函数，
// 每个 OnHook 可以修改 session 状态、调用外部命令或更新 UI。
// passObjs 是跨 hook 传递的对象列表，hook 可以通过返回值往里追加。
func ExecToolOnHook(session *structs.Chats, name string, args map[string]*any, toolID string) error {
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
		if hook.OnHook.Func != nil {
			pass, passObj, err := hook.OnHook.Func(session, args, passObjs, toolID)
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
	}
	// logger.Error("all tool passed")
	return nil
}

// ExecToolPostHook 执行工具的 PostHook 回调，收集工具执行后的返回数据。
// 与 OnHook 不同，PostHook 的返回值会作为工具调用的响应内容被持久化和返回给 LLM。
// 各 hook 按 Priority 降序执行，首个返回非 continue 的结果即为最终返回值。
func ExecToolPostHook(session *structs.Chats, name string, args map[string]*any, toolID string) (map[string]*any, error) {
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
		if hook.PostHook.Func != nil {
			idAny := any(toolID)
			// 将工具调用 ID 注入参数，供 PostHook 的日志或追踪逻辑使用
			args["_id"] = &idAny
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
	}
	// logger.Error("all tool passed")
	return map[string]*any{}, nil
}
