package prompts

import "text/template"

// GlobalTemplate 思考模板
var GlobalTemplate *template.Template

// ThinkingWrapTemplate 思考模板
var ThinkingWrapTemplate *template.Template

// ToolsWrapTemplate 工具模板
var ToolsWrapTemplate *template.Template

// SummaryWrapTemplate 总结模板
var SummaryWrapTemplate *template.Template

func init() {
	GlobalTemplate = template.Must(template.New("Global").Parse(Global))
	ThinkingWrapTemplate = template.Must(template.New("ThinkingWrap").Parse(ThinkingWrap))
	ToolsWrapTemplate = template.Must(template.New("ToolsWrap").Parse(ToolsWrap))
	SummaryWrapTemplate = template.Must(template.New("SummaryWrap").Parse(SummaryWrap))
}
