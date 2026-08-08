package config

import (
	"encoding/json"
	"testing"

	"github.com/cxykevin/alkaid0/config/structs"
)

// TestEnsureOnlineSearchDefaultsAllZero 全新配置（无既有配置 JSON）应填充期望
// 默认值：Bing 默认启用，GitHub/arXiv/Tavily 默认关闭，超时/重试/结果数按期望值。
func TestEnsureOnlineSearchDefaultsAllZero(t *testing.T) {
	cfg := &structs.Config{}
	EnsureOnlineSearchDefaults(cfg, nil)

	on := cfg.Context.OnlineSearch
	if on.Timeout != 30 {
		t.Errorf("Timeout = %d, want 30", on.Timeout)
	}
	if on.RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", on.RetryCount)
	}
	if !on.Bing.Enable {
		t.Error("Bing.Enable should be true by default")
	}
	if on.Bing.MinDelay != 2 || on.Bing.MaxDelay != 5 || on.Bing.MaxResults != 10 {
		t.Errorf("Bing defaults wrong: min=%d max=%d results=%d, want 2/5/10",
			on.Bing.MinDelay, on.Bing.MaxDelay, on.Bing.MaxResults)
	}
	if on.Github.Enable || on.Arxiv.Enable || on.Tavily.Enable {
		t.Error("Github/Arxiv/Tavily should be disabled by default")
	}
	if on.Github.MaxResults != 5 || on.Arxiv.MaxResults != 5 {
		t.Errorf("MaxResults = github:%d arxiv:%d, want 5/5", on.Github.MaxResults, on.Arxiv.MaxResults)
	}
	if on.Tavily.SearchDepth != "basic" || on.Tavily.MaxResults != 10 {
		t.Errorf("Tavily defaults wrong: depth=%q results=%d, want basic/10",
			on.Tavily.SearchDepth, on.Tavily.MaxResults)
	}
}

// TestEnsureOnlineSearchDefaultsKeepsExplicit 用户显式配置的值（含 Enable=false）
// 应保留，只填充配置中未出现过的缺失字段。
func TestEnsureOnlineSearchDefaultsKeepsExplicit(t *testing.T) {
	// 配置文件显式设置：Timeout=60、Bing.Enable=false（显式关闭）、Github.Enable=true。
	raw := json.RawMessage(`{
		"Context":{"OnlineSearch":{
			"timeout":60,
			"bing":{"enable":false},
			"github":{"enable":true}
		}}
	}`)
	cfg := &structs.Config{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	EnsureOnlineSearchDefaults(cfg, raw)

	on := cfg.Context.OnlineSearch
	if on.Timeout != 60 {
		t.Errorf("explicit Timeout=60 lost, got %d", on.Timeout)
	}
	if on.Bing.Enable {
		t.Error("explicit Bing.Enable=false should be preserved")
	}
	// Bing 未写过的字段仍应填充默认值。
	if on.Bing.MinDelay != 2 || on.Bing.MaxDelay != 5 || on.Bing.MaxResults != 10 {
		t.Errorf("Bing missing fields not filled: min=%d max=%d results=%d, want 2/5/10",
			on.Bing.MinDelay, on.Bing.MaxDelay, on.Bing.MaxResults)
	}
	if !on.Github.Enable {
		t.Error("explicit Github.Enable=true should be preserved")
	}
	if on.Github.MaxResults != 5 {
		t.Errorf("Github.MaxResults = %d, want 5", on.Github.MaxResults)
	}
}
