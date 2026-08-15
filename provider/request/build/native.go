package build

import (
	"sort"

	"github.com/cxykevin/alkaid0/provider/parser"
)

// ToolParametersToJSONSchema 将内部 map[string]parser.ToolParameters 转为 OpenAI JSON Schema 对象。
// 根为 {"type":"object","properties":{...},"required":[...]}；required 排序保证确定性；
// description 非空才写。当前 ToolParameters 无嵌套 schema，object/array 参数生成浅 schema
// （{"type":"object"} 无 properties），OpenAI 允许自由对象，可接受。
//
// 当前轮工具定义使用此函数将内部参数转换为 OpenAI JSON Schema。
func ToolParametersToJSONSchema(params map[string]parser.ToolParameters) map[string]any {
	properties := make(map[string]any, len(params))
	required := make([]string, 0, len(params))
	for name, p := range params {
		prop := map[string]any{"type": string(p.Type)}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		properties[name] = prop
		if p.Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
