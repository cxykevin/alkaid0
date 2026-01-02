package build

import (
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/tools"
	"github.com/cxykevin/alkaid0/tools/toolobj"
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
func Tools() (string, string, *[]*parser.ToolsDefine) {
	scopesString := prompts.Render(prompts.ToolScopesTemplate, struct {
		Scopes []scopeInfo
	}{
		Scopes: map2Slice(toolobj.Scopes, func(k string, v string) *scopeInfo {
			enabled := false
			if val, ok := toolobj.EnableScopes[k]; ok {
				enabled = val
			}
			return &scopeInfo{
				ID:          k,
				Description: v,
				Enable:      enabled,
			}
		}),
	})

	globalToolsTracesUnused, globalToolsTracesActive, _ := tools.ExecOneToolGetPrompts("")

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
		if val, ok := toolobj.EnableScopes[v.Scope]; !ok || !val {
			continue
		}
		unusedPrompt, activePrompt, paras := tools.ExecOneToolGetPrompts(k)
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

// ToolsSolver 构建工具处理器
func ToolsSolver(callback func(string, string, map[string]*any) error) *[]*parser.ToolsDefine {

	toolsDef := make([]*parser.ToolsDefine, 0)
	for k, v := range toolobj.ToolsList {
		toolDefObj := &parser.ToolsDefine{
			Name: k,
			Func: func(ID string, arg map[string]*any, ok bool) error {
				if !ok {
					err := tools.ExecToolOnHook(k, arg)
					if err != nil {
						return err
					}
					return nil
				}
				ret, err := tools.ExecToolPostHook(k, arg)
				if err != nil {
					return err
				}
				err = callback(k, ID, ret)
				if err != nil {
					return err
				}
				return nil
			},
		}
		if k == "" {
			continue
		}
		if val, ok := toolobj.EnableScopes[v.Scope]; !ok || !val {
			continue
		}
		_, _, paras := tools.ExecOneToolGetPrompts(k)
		toolDefObj.Parameters = paras
		toolsDef = append(toolsDef, toolDefObj)
	}

	return &toolsDef
}
