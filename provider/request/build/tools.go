package build

import (
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
func Tools(session *structs.Chats) (string, string, *[]*parser.ToolsDefine) {
	scopesString := prompts.Render(prompts.ToolScopesTemplate, struct {
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

	globalToolsTracesUnused, globalToolsTracesActive, _ := tools.ExecOneToolGetPrompts(session, "")

	globalToolTraceStr := prompts.Render(prompts.ToolPrehookTemplate, struct {
		Unused []string
		Active []string
	}{
		Unused: globalToolsTracesUnused,
		Active: globalToolsTracesActive,
	})

	toolsDef := make([]*parser.ToolsDefine, 0)
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
		toolDefObj := &parser.ToolsDefine{
			Name: k,
			Description: prompts.Render(prompts.ToolPrehookTemplate, struct {
				Unused []string
				Active []string
			}{
				Unused: unusedPrompt,
				Active: activePrompt,
			}),
		}
		toolDefObj.Parameters = paras
		toolsDef = append(toolsDef, toolDefObj)
	}

	return scopesString, globalToolTraceStr, &toolsDef
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
					err := tools.ExecToolOnHook(session, toolKey, arg)
					if err != nil {
						return err
					}
					return nil
				}
				if session.State != state.StateToolCalling {
					return nil
				}
				ret, err := tools.ExecToolPostHook(session, toolKey, arg)
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

	return &toolsDef
}
