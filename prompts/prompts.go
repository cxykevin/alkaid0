package prompts

import _ "embed" // 嵌入提示词

// Global 全局提示词
//
//go:embed prompts/global.md
var Global string

// Tools 工具提示词
//
//go:embed prompts/tools.md
var Tools string

// ToolsWrap 工具调用占位符
//
//go:embed prompts/tools_wrap.md
var ToolsWrap string

// ThinkingWrap 对于不支持思考的模型的思考占位符
//
//go:embed prompts/thinking_wrap.md
var ThinkingWrap string

// SummaryWrap 对于不支持思考的模型的思考占位符
//
//go:embed prompts/summary_wrap.md
var SummaryWrap string

// DefaultAgent 默认 Agent 的提示词
//
//go:embed prompts/default_agent.md
var DefaultAgent string

// Summary 总结提示词
//
//go:embed prompts/summary.md
var Summary string

// UserPromptWrap 用户提示词
//
//go:embed prompts/user_wrap.md
var UserPromptWrap string
