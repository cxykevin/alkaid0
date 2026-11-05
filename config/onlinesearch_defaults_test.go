package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const wantSearchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// TestLoadOnlineSearchDefaults 配置里完全没写 Context.OnlineSearch 时，
// 各 provider 都要拿到 default tag 里的默认值（Bing 默认启用）。
func TestLoadOnlineSearchDefaults(t *testing.T) {
	cfg := applyLoadPipeline(t, `{}`)
	on := cfg.Context.OnlineSearch

	if on.Timeout != 30 || on.RetryCount != 3 {
		t.Errorf("timeout/retry = %d/%d, want 30/3", on.Timeout, on.RetryCount)
	}
	if !on.Bing.Enable || on.Bing.UserAgent != wantSearchUserAgent ||
		on.Bing.MinDelay != 2 || on.Bing.MaxDelay != 5 || on.Bing.MaxResults != 10 {
		t.Errorf("bing defaults lost: %+v", on.Bing)
	}
	if on.Github.MaxResults != 5 || on.Arxiv.MaxResults != 5 {
		t.Errorf("github/arxiv max_results = %d/%d, want 5/5", on.Github.MaxResults, on.Arxiv.MaxResults)
	}
	if on.Tavily.SearchDepth != "basic" || on.Tavily.MaxResults != 10 {
		t.Errorf("tavily defaults lost: %+v", on.Tavily)
	}
	if on.DeepSeek.Enable || on.DeepSeek.MaxResults != 10 || on.DeepSeek.NeedContent {
		t.Errorf("deepseek defaults lost: %+v", on.DeepSeek)
	}
	if on.Zread.Locale != "zh" || on.Zread.UserAgent != wantSearchUserAgent {
		t.Errorf("zread defaults lost: %+v", on.Zread)
	}
	if on.Brave.Country != "US" || on.Brave.Safesearch != "moderate" || on.Brave.MaxResults != 10 {
		t.Errorf("brave defaults lost: %+v", on.Brave)
	}
}

// TestLoadOnlineSearchPartialConfig 只写一部分字段时：显式值（含 false / 0）优先，
// 其余字段仍回落默认值；provider 写成空对象或 null 视为整体未配置。
func TestLoadOnlineSearchPartialConfig(t *testing.T) {
	cfg := applyLoadPipeline(t, `{"Context":{"OnlineSearch":{
		"timeout":60,
		"bing":{"enable":false},
		"github":{"enable":true,"max_results":0},
		"deepseek":{"enable":true,"max_results":3,"need_content":true,"api_key":"sk-x"},
		"tavily":{},
		"context7":null}}}`)
	on := cfg.Context.OnlineSearch

	if on.Timeout != 60 {
		t.Errorf("Timeout = %d, want explicit 60", on.Timeout)
	}
	if on.Bing.Enable {
		t.Error("explicit Bing.Enable=false should be kept")
	}
	if on.Bing.UserAgent != wantSearchUserAgent || on.Bing.MaxResults != 10 {
		t.Errorf("bing missing fields should fall back to defaults: %+v", on.Bing)
	}
	if !on.Github.Enable || on.Github.MaxResults != 0 {
		t.Errorf("github explicit values lost: %+v", on.Github)
	}
	if !on.DeepSeek.Enable || on.DeepSeek.MaxResults != 3 || !on.DeepSeek.NeedContent ||
		on.DeepSeek.APIKey != "sk-x" {
		t.Errorf("deepseek explicit values lost: %+v", on.DeepSeek)
	}
	if on.Tavily.SearchDepth != "basic" || on.Tavily.MaxResults != 10 {
		t.Errorf("empty provider object should get defaults: %+v", on.Tavily)
	}
	if on.Context7.Enable || on.Context7.Fast || on.Context7.MaxResults != 0 {
		t.Errorf("null provider should get defaults: %+v", on.Context7)
	}
}

// TestLoadOnlineSearchDefaultsEndToEnd 端到端：从配置文件 Load 后，序列化出的
// 配置（config/get 与落盘同源）必须包含每个搜索源的 enable 字段。
func TestLoadOnlineSearchDefaultsEndToEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"Version":1,"Context":{"OnlineSearch":{"deepseek":{"enable":true}}}}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, path)
	oldPath := configPath
	defer func() { configPath = oldPath }()
	configPath = ""

	Load()

	snapshot, err := SnapshotJSON()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var all map[string]any
	if err := json.Unmarshal(snapshot, &all); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	ctx, ok := all["Context"].(map[string]any)
	if !ok {
		t.Fatalf("Context missing from snapshot: %v", all["Context"])
	}
	on, ok := ctx["OnlineSearch"].(map[string]any)
	if !ok {
		t.Fatalf("OnlineSearch missing from snapshot")
	}
	providers := make(map[string]map[string]any, len(on))
	for name, v := range on {
		if p, ok := v.(map[string]any); ok {
			providers[name] = p
		}
	}
	if len(providers) != 15 {
		t.Fatalf("OnlineSearch has %d providers, want 15", len(providers))
	}
	for name, p := range providers {
		if _, ok := p["enable"]; !ok {
			t.Errorf("provider %q has no enable key in config JSON: %v", name, p)
		}
	}
	if v := providers["deepseek"]["enable"]; v != true {
		t.Errorf("deepseek.enable = %v, want true", v)
	}
	if v := providers["bing"]["enable"]; v != true {
		t.Errorf("bing.enable = %v, want true (default)", v)
	}
	if v := providers["tavily"]["enable"]; v != false {
		t.Errorf("tavily.enable = %v, want false", v)
	}

	// 未用到 structs 之外的解析：确保 GlobalConfig 与快照一致
	if !GlobalConfig.Context.OnlineSearch.Bing.Enable {
		t.Error("GlobalConfig Bing.Enable should be true")
	}
}
