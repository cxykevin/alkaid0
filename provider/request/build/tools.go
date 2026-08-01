package build

import (
	"fmt"

	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/ui/state"
)

type scopeInfo struct {
	ID          string
	Description string
	Enable      bool
}

func map2Slice[sliceType any, originMapKeyType comparable, originMapValType any](origin map[originMapKeyType]originMapValType, filter func(originMapKeyType, originMapValType) *sliceType) []sliceType {
	lists := make([]sliceType, 0)
	for k, v := range origin {
		ret := filter(k, v)
		if ret != nil {
			lists = append(lists, *ret)
		}
	}
	return lists
}

// Tools 构建工具(scopes, tool traces, tools)
// 渲染失败时返回请求级错误而非 panic，避免拖垮整个进程
func Tools(session *structs.Chats) (string, string, *[]*parser.ToolsDefine, error) {
	toolScopesRendered, err := prompts.Render(prompts.ToolScopesTemplate, struct {
		Scopes []scopeInfo
	}{
		Scopes: map2Slice(toolobj.Scopes, func(k string, v string) *scopeInfo {
			enabled := false
			if val, ok := session.EnableScopes[k]; ok {
				enabled = val
			}
			return &scopeInfo{
				ID:          k,
				Description: v,
				Enable:      enabled,
			}
		}),
	})
	if err != nil {
		return "", "", &[]*parser.ToolsDefine{}, err
	}
	scopesString := toolScopesRendered

	globalToolsTracesUnused, globalToolsTracesActive, _ := tools.ExecOneToolGetPrompts(session, "")

	globalToolTraceRendered, err := prompts.Render(prompts.ToolPrehookTemplate, struct {
		Unused []string
		Active []string
	}{
		Unused: globalToolsTracesUnused,
		Active: globalToolsTracesActive,
	})
	if err != nil {
		return "", "", &[]*parser.ToolsDefine{}, err
	}
	globalToolTraceStr := globalToolTraceRendered

	toolsDef := make([]*parser.ToolsDefine, 0)
	toolobj.ToolsMu.RLock()
	for k, v := range toolobj.ToolsList {
		// Global 工具不包含在总工具表中，但 hooks 已通过 globalToolTraceStr 处理
		if k == "" {
			continue
		}
		if v.Enable != nil {
			enableFlag := v.Enable(session)
			if !enableFlag {
				continue
			}
		}
		if !checkToolScope(session, v.Scope) {
			continue
		}
		unusedPrompt, activePrompt, paras := tools.ExecOneToolGetPrompts(session, k)
		toolDescription, err := prompts.Render(prompts.ToolPrehookTemplate, struct {
			Unused []string
			Active []string
		}{
			Unused: unusedPrompt,
			Active: activePrompt,
		})
		if err != nil {
			toolobj.ToolsMu.RUnlock()
			return "", "", &[]*parser.ToolsDefine{}, err
		}
		toolDefObj := &parser.ToolsDefine{
			Name:        k,
			Description: toolDescription,
		}
		toolDefObj.Parameters = paras
		toolsDef = append(toolsDef, toolDefObj)
	}

	toolobj.ToolsMu.RUnlock()
	return scopesString, globalToolTraceStr, &toolsDef, nil
}

func checkToolScope(session *structs.Chats, scope string) bool {
	if scope == "" {
		return true
	}
	if val, ok := session.EnableScopes[scope]; !ok || !val {
		return false
	}
	return true
}

// ToolsSolver 构建工具处理器
func ToolsSolver(session *structs.Chats, callback func(string, string, map[string]*any) error) *[]*parser.ToolsDefine {

	toolsDef := make([]*parser.ToolsDefine, 0)
	toolobj.ToolsMu.RLock()
	for k, v := range toolobj.ToolsList {
		if k == "" {
			continue
		}
		if v.Enable != nil {
			enableFlag := v.Enable(session)
			if !enableFlag {
				continue
			}
		}
		toolKey := k
		toolDefObj := &parser.ToolsDefine{
			Name: k,
			Func: func(ID string, arg map[string]*any, ok bool) error {
				if !ok {
					// 流式解析期间工具对象尚未完整到达（toolFinishTag=false）。
					// 恢复增量流式：流式阶段（StateReciving）也执行 OnHook，把当前部分参数
					// 写入 ToolCallingContext，由 server 层限流广播实时预览到前端。
					// 所有内置工具 OnHook 均为纯展示（构造 calling_info，无命令/文件/子代理副作用），
					// 以部分参数重复执行幂等无害；若未来 OnHook 引入真实副作用需重新评估。
					session.CurrentToolID = fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, ID)
					err := tools.ExecToolOnHook(session, toolKey, arg, ID)
					if err != nil {
						return err
					}
					return nil
				}
				if session.State != state.StateToolCalling {
					return nil
				}
				ret, err := tools.ExecToolPostHook(session, toolKey, arg, ID)
				if err != nil {
					return err
				}
				err = callback(toolKey, ID, ret)
				if err != nil {
					return err
				}
				return nil
			},
		}
		if k == "" {
			continue
		}
		if !checkToolScope(session, v.Scope) {
			continue
		}
		_, _, paras := tools.ExecOneToolGetPrompts(session, k)
		toolDefObj.Parameters = paras
		toolsDef = append(toolsDef, toolDefObj)
	}

	toolobj.ToolsMu.RUnlock()
	return &toolsDef
}
