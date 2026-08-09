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

// TestNativeToolPromptEnhanceBlock 验证原生模式增强段与 ToolPromptEnhanceBlock 共享判定逻辑，
// 仅返回文本不同（tool_enhance_native.md）。
func TestNativeToolPromptEnhanceBlock(t *testing.T) {
	cases := []struct {
		name  string
		value string
		model string
		want  bool // true=期望非空增强段
	}{
		{"auto 普通模型启用", "auto", "deepseek-v4-flash", true},
		{"空值视为 auto 启用", "", "qwen3-32b", true},
		{"auto gpt 免增强", "auto", "gpt-5", false},
		{"auto claude 免增强", "auto", "claude-3-7-sonnet", false},
		{"auto fable 免增强", "auto", "claude-fable-5", false},
		{"auto 模型id大小写不敏感", "auto", "CLAUDE-3-7-SONNET", false},
		{"on 强制开启无视名单", "on", "claude-3-7-sonnet", true},
		{"off 强制关闭", "off", "deepseek-v4-flash", false},
	}
	for _, c := range cases {
		mc := &cfgStruct.ModelConfig{
			ModelID: c.model,
			ProviderSpecificConfig: cfgStruct.ProviderSpecificConfig{
				ToolPromptEnhance: c.value,
			},
		}
		got := NativeToolPromptEnhanceBlock(mc)
		if c.want && got == "" {
			t.Errorf("%s: 原生模式期望非空增强段，实际为空", c.name)
		}
		if !c.want && got != "" {
			t.Errorf("%s: 原生模式期望空增强段，实际得到 %q", c.name, got)
		}
		if got != "" && !strings.Contains(got, "HARD REINFORCEMENT") {
			t.Errorf("%s: 原生增强段应包含 HARD REINFORCEMENT，实际: %q", c.name, got)
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

	// 原生模式：auto + 普通模型 = ToolNative 1 次 + 反向增强段 1 次
	cN := buildReq(true, "deepseek-v4-flash", "")
	if !strings.Contains(cN, "HARD REINFORCEMENT") {
		t.Error("原生模式 auto 普通模型应包含反向增强段")
	}
	if !strings.Contains(cN, "(Native)") {
		t.Error("原生模式应包含 ToolNative 基础提示词")
	}
	if n := countBase(cN); n != 1 {
		t.Errorf("原生模式 auto 普通模型基础 ToolNative 应拼接 1 次，实际 %d", n)
	}

	// 原生模式：auto + gpt 命中免增强名单 → 仅 ToolNative 1 次，无增强段
	cNGpt := buildReq(true, "gpt-5", "")
	if strings.Contains(cNGpt, "HARD REINFORCEMENT") {
		t.Error("原生模式 auto gpt 不应包含增强段")
	}
	if n := countBase(cNGpt); n != 1 {
		t.Errorf("原生模式 auto gpt 基础 ToolNative 应拼接 1 次，实际 %d", n)
	}

	// 原生模式：off 强制关闭 → 仅 ToolNative 1 次，无增强段
	cNOff := buildReq(true, "deepseek-v4-flash", "off")
	if strings.Contains(cNOff, "HARD REINFORCEMENT") {
		t.Error("原生模式 off 不应包含增强段")
	}
	if n := countBase(cNOff); n != 1 {
		t.Errorf("原生模式 off 基础 ToolNative 应拼接 1 次，实际 %d", n)
	}
}
