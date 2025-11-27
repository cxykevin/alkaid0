package prompts

import _ "embed" // 嵌入提示词

// Tools 工具提示词
//
//go:embed prompts/tools.gomd
var Tools string
