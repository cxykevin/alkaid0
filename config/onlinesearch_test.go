package config

import (
	"encoding/json"
	"testing"

	"github.com/cxykevin/alkaid0/config/structs"
)

// TestEnsureOnlineSearchDefaultsAllZero 全新配置（无既有配置 JSON）应填充期望
// 默认值：Bing 默认启用，其余 provider 默认关闭，超时/重试/结果数按期望值。
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
	// v0.0.3 新增 provider 默认全部关闭。
	newEnabled := []bool{on.Context7.Enable, on.Zread.Enable, on.Brave.Enable,
		on.Test.Enable, on.GrepApp.Enable, on.Sourcegraph.Enable,
		on.StackOverflow.Enable, on.HackerNews.Enable, on.Devto.Enable, on.LibrariesIO.Enable}
	for _, en := range newEnabled {
		if en {
			t.Error("new providers should be disabled by default")
		}
	}
	// 非零默认字段与库 DefaultConfig 对齐。
	if on.Zread.Locale != "zh" || on.Zread.UserAgent == "" ||
		on.Zread.MinDelay != 2 || on.Zread.MaxDelay != 5 {
		t.Errorf("Zread defaults wrong: locale=%q ua=%q min=%d max=%d, want zh/<non-empty>/2/5",
			on.Zread.Locale, on.Zread.UserAgent, on.Zread.MinDelay, on.Zread.MaxDelay)
	}
	if on.Brave.Country != "US" || on.Brave.SearchLang != "en" || on.Brave.UILang != "en-US" ||
		on.Brave.Safesearch != "moderate" || on.Brave.MaxResults != 10 {
		t.Errorf("Brave defaults wrong: country=%q lang=%q ui=%q safe=%q results=%d, want US/en/en-US/moderate/10",
			on.Brave.Country, on.Brave.SearchLang, on.Brave.UILang, on.Brave.Safesearch, on.Brave.MaxResults)
	}
	newMaxResults := []int{on.StackOverflow.MaxResults, on.HackerNews.MaxResults,
		on.Devto.MaxResults, on.LibrariesIO.MaxResults}
	for _, mr := range newMaxResults {
		if mr != 10 {
			t.Errorf("new provider MaxResults = %d, want 10", mr)
		}
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

// TestEnsureOnlineSearchDefaultsNewProvidersKeepsExplicit 用户显式启用 v0.0.3 新增
// provider 并写部分字段时，显式值应保留，未写字段应填充默认值。
func TestEnsureOnlineSearchDefaultsNewProvidersKeepsExplicit(t *testing.T) {
	raw := json.RawMessage(`{
		"Context":{"OnlineSearch":{
			"zread":{"enable":true,"locale":"en"},
			"brave":{"enable":true,"api_key":"BSA-xxx"}
		}}
	}`)
	cfg := &structs.Config{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	EnsureOnlineSearchDefaults(cfg, raw)

	on := cfg.Context.OnlineSearch
	if !on.Zread.Enable {
		t.Error("explicit Zread.Enable=true should be preserved")
	}
	if on.Zread.Locale != "en" {
		t.Errorf("explicit Zread.Locale=en lost, got %q", on.Zread.Locale)
	}
	// Zread 未写过的字段仍应填充默认值。
	if on.Zread.MinDelay != 2 || on.Zread.MaxDelay != 5 || on.Zread.UserAgent == "" {
		t.Errorf("Zread missing fields not filled: min=%d max=%d ua=%q",
			on.Zread.MinDelay, on.Zread.MaxDelay, on.Zread.UserAgent)
	}
	if !on.Brave.Enable {
		t.Error("explicit Brave.Enable=true should be preserved")
	}
	if on.Brave.APIKey != "BSA-xxx" {
		t.Errorf("explicit Brave.APIKey lost, got %q", on.Brave.APIKey)
	}
	if on.Brave.Country != "US" || on.Brave.Safesearch != "moderate" || on.Brave.MaxResults != 10 {
		t.Errorf("Brave missing fields not filled: country=%q safe=%q results=%d",
			on.Brave.Country, on.Brave.Safesearch, on.Brave.MaxResults)
	}
	// 未出现的 provider 仍保持默认关闭。
	if on.StackOverflow.Enable || on.Devto.Enable {
		t.Error("providers absent from config should stay disabled")
	}
	if on.StackOverflow.MaxResults != 10 {
		t.Errorf("absent StackOverflow.MaxResults = %d, want 10", on.StackOverflow.MaxResults)
	}
}
