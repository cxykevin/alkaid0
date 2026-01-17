package prompts

import (
	"text/template"
)

// GlobalTemplate 思考模板
var GlobalTemplate *template.Template

// DeltaWrapTemplate response 模板
var DeltaWrapTemplate *template.Template

// ToolsWrapTemplate 工具模板
var ToolsWrapTemplate *template.Template

// SummaryWrapTemplate 总结模板
var SummaryWrapTemplate *template.Template

// UserPromptTemplate 用户消息模板
var UserPromptTemplate *template.Template

// ToolPrehookTemplate 工具预调用描述模板
var ToolPrehookTemplate *template.Template

// ToolScopesTemplate 工具预调用描述模板
var ToolScopesTemplate *template.Template

// ToolResponseWrapTemplate 工具响应描述模板（更推荐走 trace）
var ToolResponseWrapTemplate *template.Template

// ToolsCallWrapTemplate 工具调用模板
var ToolsCallWrapTemplate *template.Template

func init() {
	// 创建函数映射
	funcMap := template.FuncMap{
		"toInt":  toInt,
		"sub":    sub,
		"string": toString,
		"le":     le,
		"gt":     gt,
	}

	GlobalTemplate = template.Must(template.New("Global").Funcs(funcMap).Parse(Global))
	DeltaWrapTemplate = template.Must(template.New("DeltaWrap").Funcs(funcMap).Parse(DeltaWrap))
	ToolsWrapTemplate = template.Must(template.New("ToolsWrap").Funcs(funcMap).Parse(ToolsWrap))
	SummaryWrapTemplate = template.Must(template.New("SummaryWrap").Funcs(funcMap).Parse(SummaryWrap))
	UserPromptTemplate = template.Must(template.New("UserPrompt").Funcs(funcMap).Parse(UserPromptWrap))
	ToolPrehookTemplate = template.Must(template.New("ToolPrehook").Funcs(funcMap).Parse(ToolPrehook))
	ToolScopesTemplate = template.Must(template.New("ToolScopes").Funcs(funcMap).Parse(ToolScopes))
	ToolResponseWrapTemplate = template.Must(template.New("ToolResponseWrap").Funcs(funcMap).Parse(ToolResponseWrap))
}
