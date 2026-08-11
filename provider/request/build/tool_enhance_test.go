package build

import (
	"strings"
	"testing"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
)

func TestToolPromptEnhanceBlock(t *testing.T) {
	cases := []struct {
		name  string
		value string
		model string
		want  string // "" 表示期望空；否则期望包含该子串
	}{
		{"auto 普通模型启用", "auto", "deepseek-v4-flash", "TOOL CALLING"},
		{"空值视为 auto 启用", "", "qwen3-32b", "TOOL CALLING"},
		{"auto gpt 免增强", "auto", "gpt-5", ""},
		{"auto claude 免增强", "auto", "claude-3-7-sonnet", ""},
		{"auto fable 免增强", "auto", "claude-fable-5", ""},
		{"auto sonnet 免增强", "auto", "claude-sonnet-5", ""},
		{"auto haiku 免增强", "auto", "claude-haiku-4-5", ""},
		{"auto opus 免增强", "auto", "claude-opus-5", ""},
		{"auto sol 免增强", "auto", "sol-1", ""},
		{"auto luna 免增强", "auto", "luna-2", ""},
		{"auto terra 免增强", "auto", "terra-3", ""},
		{"auto 模型id大小写不敏感", "auto", "CLAUDE-3-7-SONNET", ""},
		{"on 强制开启无视名单", "on", "claude-3-7-sonnet", "TOOL CALLING"},
		{"off 强制关闭", "off", "deepseek-v4-flash", ""},
		{"off 大小写不敏感", "OFF", "gpt-5", ""},
	}
	for _, c := range cases {
		mc := &cfgStruct.ModelConfig{
			ModelID: c.model,
			ProviderSpecificConfig: cfgStruct.ProviderSpecificConfig{
				ToolPromptEnhance: c.value,
			},
		}
		got := ToolPromptEnhanceBlock(mc)
		if c.want == "" {
			if got != "" {
				t.Errorf("%s: 期望空增强段，实际得到 %q", c.name, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: 增强段应包含 %q，实际: %q", c.name, c.want, got)
		}
	}
}

func TestToolPromptEnhanceBlockNil(t *testing.T) {
	if got := ToolPromptEnhanceBlock(nil); got != "" {
		t.Errorf("nil 配置应返回空串，实际: %q", got)
	}
	if got := NativeToolPromptEnhanceBlock(nil); got != "" {
		t.Errorf("Native nil 配置应返回空串，实际: %q", got)
	}
}

// TestNativeToolPromptEnhanceBlockDisabled 验证原生反幻觉增强当前处于禁用状态：
// tool_enhance_native.md 仅保留英文模板注释、无增强正文，无论 auto/on/off 配置，
// 函数返回的段落都不含 HARD REINFORCEMENT 强化文本。
// 恢复增强时：恢复 tool_enhance_native.md 的正文 + 取消 build/request.go 中的调用注释，
// 并将本测试改回断言 HARD REINFORCEMENT 语义。
func TestNativeToolPromptEnhanceBlockDisabled(t *testing.T) {
	cases := []struct {
		name  string
		value string
		model string
	}{
		{"auto 普通模型", "auto", "deepseek-v4-flash"},
		{"空值视为 auto", "", "qwen3-32b"},
		{"auto gpt 免增强", "auto", "gpt-5"},
		{"on 强制开启", "on", "claude-3-7-sonnet"},
		{"off 强制关闭", "off", "deepseek-v4-flash"},
	}
	for _, c := range cases {
		mc := &cfgStruct.ModelConfig{
			ModelID: c.model,
			ProviderSpecificConfig: cfgStruct.ProviderSpecificConfig{
				ToolPromptEnhance: c.value,
			},
		}
		if got := NativeToolPromptEnhanceBlock(mc); strings.Contains(got, "HARD REINFORCEMENT") {
			t.Errorf("%s: 增强已禁用，期望无强化正文，实际得到 %q", c.name, got)
		}
	}
}

