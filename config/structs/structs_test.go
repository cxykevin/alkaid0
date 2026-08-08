package structs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildDefault(t *testing.T) {
	type TestStruct struct {
		Name  string  `default:"test"`
		Age   int     `default:"20"`
		Score float64 `default:"95.5"`
		Valid bool    `default:"true"`
	}

	ts := BuildDefault(TestStruct{})
	if ts.Name != "test" {
		t.Errorf("Expected Name 'test', got %s", ts.Name)
	}
	if ts.Age != 20 {
		t.Errorf("Expected Age 20, got %d", ts.Age)
	}
	if ts.Score != 95.5 {
		t.Errorf("Expected Score 95.5, got %f", ts.Score)
	}
	if ts.Valid != true {
		t.Errorf("Expected Valid true, got %v", ts.Valid)
	}
}

func TestModelsConfig(t *testing.T) {
	mc := ModelsConfig{}
	mc = BuildDefault(mc)
	if mc.ProviderURL == "" {
		t.Error("ProviderURL should not be empty after BuildDefault")
	}
}

func TestEmptyContainers(t *testing.T) {
	c := BuildDefault(Config{})
	// map 字段应初始化为空 map（JSON 序列化为 {} 而非 null）
	if c.Model.Models == nil {
		t.Error("Model.Models should be initialized to empty map")
	}
	if len(c.Model.Models) != 0 {
		t.Errorf("Model.Models should be empty, got len %d", len(c.Model.Models))
	}
	if c.Agent.Agents == nil {
		t.Error("Agent.Agents should be initialized to empty map")
	}
	if c.Context.LSP.LanguageServers == nil {
		t.Error("Context.LSP.LanguageServers should be initialized to empty map")
	}
	if c.Agent.TerminalEnvs == nil {
		t.Error("Agent.TerminalEnvs should be initialized to empty map")
	}
	// slice 字段应初始化为空 slice（JSON 序列化为 [] 而非 null）
	if c.Context.Phrase.Phrases == nil {
		t.Error("Context.Phrase.Phrases should be initialized to empty slice")
	}
	if len(c.Context.Phrase.Phrases) != 0 {
		t.Errorf("Context.Phrase.Phrases should be empty, got len %d", len(c.Context.Phrase.Phrases))
	}

	// 序列化验证：Agents/LanguageServers/Models 输出 {}，Phrases 输出 []
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	s := string(data)
	for _, key := range []string{"\"Agents\":{}", "\"LanguageServers\":{}", "\"Models\":{}", "\"Phrases\":[]"} {
		if !strings.Contains(s, key) {
			t.Errorf("JSON output should contain %s, got: %s", key, s)
		}
	}
}

func TestDataMaskDefaults(t *testing.T) {
	c := BuildDefault(Config{})
	if !c.DataMask.Enable {
		t.Error("DataMask.Enable should default to true")
	}
	// 默认仅脱敏 apikey（含长 token）与 jwt；手机号/IP/session 默认关闭，避免误伤
	if !c.DataMask.MaskAPIKey {
		t.Error("DataMask.MaskAPIKey should default to true")
	}
	if !c.DataMask.MaskJWT {
		t.Error("DataMask.MaskJWT should default to true")
	}
	if c.DataMask.MaskPhone {
		t.Error("DataMask.MaskPhone should default to false")
	}
	if c.DataMask.MaskIP {
		t.Error("DataMask.MaskIP should default to false")
	}
	if c.DataMask.MaskSession {
		t.Error("DataMask.MaskSession should default to false")
	}
}
