package build

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
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

// TestRequestBodyToolEnhance 验证请求级 system prompt 拼接（模式感知）：
//   - 提示词模式：基础 tools 恒为两次拼接——auto 普通模型 = 基础+增强段；
//     auto 命中免增强名单或 off = 基础+基础；on 强制 = 基础+增强段。
//   - 原生模式：基础 ToolNative 拼接一次——auto 普通模型 = ToolNative+反向增强段；
//     auto 命中免增强名单或 off = 仅 ToolNative；on 强制 = ToolNative+反向增强段。
func TestRequestBodyToolEnhance(t *testing.T) {
	db := setupTestDB(t)
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "hi"}).Error; err != nil {
		t.Fatalf("create message failed: %v", err)
	}
	toolsList := []*parser.ToolsDefine{}

	buildReq := func(native bool, modelID, enhance string) string {
		setupTestConfig()
		cfg := *config.GlobalConfig
		m := cfg.Model.Models[cfg.Model.DefaultModelID]
		m.EnableToolCalling = native
		if modelID != "" {
			m.ModelID = modelID
		}
		if enhance != "" {
			m.ProviderSpecificConfig.ToolPromptEnhance = enhance
		}
		cfg.Model.Models[cfg.Model.DefaultModelID] = m
		config.GlobalConfigSwap(cfg)
		req, err := RequestBody(1, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
		if err != nil {
			t.Fatalf("RequestBody failed: %v", err)
		}
		return req.Messages[0].Content
	}

	countBase := func(c string) int { return strings.Count(c, "# Tool Calling Protocol") }

	// 提示词模式：auto + 普通模型 = 基础 1 次 + 增强段 1 次
	c := buildReq(false, "deepseek-v4-flash", "")
	if !strings.Contains(c, "HARD REINFORCEMENT") {
		t.Error("提示词模式 auto 普通模型应包含增强段")
	}
	if n := countBase(c); n != 1 {
		t.Errorf("提示词模式 auto 普通模型基础 tools 应拼接 1 次，实际 %d", n)
	}

	// 提示词模式：auto + gpt 命中免增强名单 → 基础仍拼接两次
	cGpt := buildReq(false, "gpt-5", "")
	if strings.Contains(cGpt, "HARD REINFORCEMENT") {
		t.Error("提示词模式 auto gpt 不应包含增强段")
	}
	if n := countBase(cGpt); n != 2 {
		t.Errorf("提示词模式 auto gpt 基础 tools 应拼接 2 次，实际 %d", n)
	}

	// 提示词模式：off 强制关闭 → 基础仍拼接两次
	cOff := buildReq(false, "deepseek-v4-flash", "off")
	if strings.Contains(cOff, "HARD REINFORCEMENT") {
		t.Error("提示词模式 off 不应包含增强段")
	}
	if n := countBase(cOff); n != 2 {
		t.Errorf("提示词模式 off 基础 tools 应拼接 2 次，实际 %d", n)
	}

	// 原生模式：增强已禁用(调试期) — 无论 auto/on/off，均只拼 ToolNative 1 次，无增强段
	for _, tc := range []struct {
		modelID, enhance string
	}{
		{"deepseek-v4-flash", ""}, // auto + 普通模型
		{"gpt-5", ""},             // auto + 命中免增强名单
		{"deepseek-v4-flash", "off"},
		{"claude-3-7-sonnet", "on"}, // on 强制开启也不再附加
	} {
		reqStr := buildReq(true, tc.modelID, tc.enhance)
		if strings.Contains(reqStr, "HARD REINFORCEMENT") {
			t.Errorf("原生模式 %s/%s 增强已禁用，不应包含增强段", tc.modelID, tc.enhance)
		}
		if !strings.Contains(reqStr, "(Native)") {
			t.Errorf("原生模式 %s/%s 应包含 ToolNative 基础提示词", tc.modelID, tc.enhance)
		}
		if n := countBase(reqStr); n != 1 {
			t.Errorf("原生模式 %s/%s 基础 ToolNative 应拼接 1 次，实际 %d", tc.modelID, tc.enhance, n)
		}
	}
}
