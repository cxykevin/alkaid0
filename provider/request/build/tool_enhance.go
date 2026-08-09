package build

import (
	"strings"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/prompts"
)

// noEnhanceKeywords auto 模式下关闭增强的模型 id 关键字。
// 实测这些模型（GPT 系 + Claude 系及其代号）对提示词遵循度高、
// 无原生 tool calling 后训练幻觉，auto 下命中即免增强；可显式 "on" 强制开启。
var noEnhanceKeywords = []string{
	"gpt", "sol", "luna", "terra",
	"claude", "fable", "sonnet", "haiku", "opus",
}

// ToolPromptEnhanceBlock 返回是否应追加反幻觉增强段及文本。
//
// 由 ProviderSpecificConfig.ToolPromptEnhance 显式控制（强制开启/强制关闭）：
//   - "off": 强制关闭，返回空串
//   - "on":  强制开启，无视模型 id
//   - "auto"(默认): 启用增强；但模型 id 命中 noEnhanceKeywords 时免增强。
//     实测除 GPT / Claude 系外几乎所有模型（DeepSeek/Qwen/GLM/Kimi 等）
//     都需要增强以压制原生 tool calling 后训练导致的输出幻觉。
//
// 增强段为通用文本（prompts/prompts/tool_enhance.md），不按 provider 匹配。
func ToolPromptEnhanceBlock(mc *cfgStruct.ModelConfig) string {
	if mc == nil {
		return ""
	}
	mode := strings.ToLower(strings.TrimSpace(mc.ProviderSpecificConfig.ToolPromptEnhance))
	switch mode {
	case "off":
		return ""
	case "on":
		return prompts.ToolEnhance
	default: // "auto" / "" 均为默认增强
		id := strings.ToLower(mc.ModelID)
		for _, kw := range noEnhanceKeywords {
			if strings.Contains(id, kw) {
				return ""
			}
		}
		return prompts.ToolEnhance
	}
}

// NativeToolPromptEnhanceBlock 原生 tool_calls 模式下的增强段（prompts/prompts/tool_enhance_native.md）。
// 与 ToolPromptEnhanceBlock 共享同一套 ToolPromptEnhance auto/on/off + 模型名单判定逻辑，
// 仅返回原生模式的增强文本。其作用为提示词模式的"反向"：强化"必须使用原生 tool_calls"、
// 声明 <tools>/<tools_input>/<tools_return> 标签会被整体拒绝。
func NativeToolPromptEnhanceBlock(mc *cfgStruct.ModelConfig) string {
	if mc == nil {
		return ""
	}
	mode := strings.ToLower(strings.TrimSpace(mc.ProviderSpecificConfig.ToolPromptEnhance))
	switch mode {
	case "off":
		return ""
	case "on":
		return prompts.ToolEnhanceNative
	default: // "auto" / "" 均为默认增强
		id := strings.ToLower(mc.ModelID)
		for _, kw := range noEnhanceKeywords {
			if strings.Contains(id, kw) {
				return ""
			}
		}
		return prompts.ToolEnhanceNative
	}
}
