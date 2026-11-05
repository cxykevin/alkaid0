package structs

import (
	"encoding/json"
	"testing"

	secconfig "github.com/cxykevin/alkaid0-search-engine/config"
)

const wantUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// providerObjects 解析在线搜索 JSON 中的 provider 对象（跳过 timeout 等标量字段）。
func providerObjects(t *testing.T, raw []byte) map[string]map[string]any {
	t.Helper()
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("unmarshal online search: %v", err)
	}
	out := make(map[string]map[string]any, len(all))
	for k, v := range all {
		if m, ok := v.(map[string]any); ok {
			out[k] = m
		}
	}
	return out
}

// TestBuildDefaultOnlineSearch 在线搜索的默认值写在结构体 default tag 里，
// BuildDefault 必须逐字段填好（含 Bing 默认启用、各 provider 自有非零默认）。
func TestBuildDefaultOnlineSearch(t *testing.T) {
	on := BuildDefault(Config{}).Context.OnlineSearch

	if on.Timeout != 30 || on.RetryCount != 3 || on.ProxyURL != "" {
		t.Errorf("global defaults = %d/%d/%q, want 30/3/\"\"", on.Timeout, on.RetryCount, on.ProxyURL)
	}
	if !on.Bing.Enable || on.Bing.UserAgent != wantUserAgent || on.Bing.MinDelay != 2 ||
		on.Bing.MaxDelay != 5 || on.Bing.MaxResults != 10 {
		t.Errorf("bing defaults lost: %+v", on.Bing)
	}
	if on.Github.Enable || on.Github.MaxResults != 5 {
		t.Errorf("github defaults = %+v", on.Github)
	}
	if on.Arxiv.Enable || on.Arxiv.MaxResults != 5 {
		t.Errorf("arxiv defaults = %+v", on.Arxiv)
	}
	if on.Tavily.Enable || on.Tavily.SearchDepth != "basic" || on.Tavily.MaxResults != 10 ||
		on.Tavily.IncludeAnswer || on.Tavily.IncludeRaw {
		t.Errorf("tavily defaults = %+v", on.Tavily)
	}
	if on.DeepSeek.Enable || on.DeepSeek.MaxResults != 10 || on.DeepSeek.NeedContent ||
		on.DeepSeek.Localization != "" {
		t.Errorf("deepseek defaults = %+v", on.DeepSeek)
	}
	if on.Context7.Enable || on.Context7.Fast || on.Context7.MaxResults != 0 {
		t.Errorf("context7 defaults = %+v", on.Context7)
	}
	if on.Zread.Locale != "zh" || on.Zread.UserAgent != wantUserAgent ||
		on.Zread.MinDelay != 2 || on.Zread.MaxDelay != 5 {
		t.Errorf("zread defaults = %+v", on.Zread)
	}
	if on.Brave.Country != "US" || on.Brave.SearchLang != "en" || on.Brave.UILang != "en-US" ||
		on.Brave.Safesearch != "moderate" || on.Brave.MaxResults != 10 || on.Brave.ExtraSnippets {
		t.Errorf("brave defaults = %+v", on.Brave)
	}
	for name, got := range map[string]int{
		"stackoverflow": on.StackOverflow.MaxResults,
		"hackernews":    on.HackerNews.MaxResults,
		"devto":         on.Devto.MaxResults,
		"libraries_io":  on.LibrariesIO.MaxResults,
	} {
		if got != 10 {
			t.Errorf("%s.max_results = %d, want 10", name, got)
		}
	}
}

// TestOnlineSearchJSONIsComplete 我们自己的配置结构不带 omitempty：每个 provider
// 的每个字段（含 enable=false / 0 / 空串）都要出现在 JSON 里，客户端配置树才
// 能显示并修改它们。
func TestOnlineSearchJSONIsComplete(t *testing.T) {
	on := BuildDefault(Config{}).Context.OnlineSearch
	raw, err := json.Marshal(on)
	if err != nil {
		t.Fatalf("marshal online search: %v", err)
	}
	obj := providerObjects(t, raw)

	providers := []string{
		"bing", "github", "arxiv", "tavily", "deepseek", "context7", "zread",
		"brave", "test", "grepapp", "sourcegraph", "stackoverflow", "hackernews",
		"devto", "libraries_io",
	}
	for _, name := range providers {
		p, ok := obj[name]
		if !ok {
			t.Errorf("provider %q missing from JSON", name)
			continue
		}
		if _, ok := p["enable"]; !ok {
			t.Errorf("provider %q has no enable key: %v", name, p)
		}
	}
	// 关键布尔/零值字段也必须出现
	if v, ok := obj["deepseek"]["need_content"]; !ok || v != false {
		t.Errorf("deepseek.need_content = %v (present=%v), want false", v, ok)
	}
	if _, ok := obj["deepseek"]["localization"]; !ok {
		t.Error("deepseek.localization missing")
	}
	if _, ok := obj["tavily"]["include_answer"]; !ok {
		t.Error("tavily.include_answer missing")
	}
	if _, ok := obj["context7"]["fast"]; !ok {
		t.Error("context7.fast missing")
	}
	if _, ok := obj["brave"]["extra_snippets"]; !ok {
		t.Error("brave.extra_snippets missing")
	}
	if v, ok := obj["github"]["max_results"]; !ok || v != float64(5) {
		t.Errorf("github.max_results = %v (present=%v), want 5", v, ok)
	}
}

// TestEngineConfigMatchesLibraryDefaults 默认值口径必须与上游 DefaultConfig 对齐
// （另加产品策略：Bing 启用+10 条、GitHub/arXiv 5 条），转换后搜索引擎拿到的配置
// 与 secconfig.DefaultConfig 一致。
func TestEngineConfigMatchesLibraryDefaults(t *testing.T) {
	want := secconfig.DefaultConfig()
	want.Bing.Enable = true
	want.Bing.MaxResults = 10
	want.Github.MaxResults = 5
	want.Arxiv.MaxResults = 5

	got := BuildDefault(Config{}).Context.OnlineSearch.EngineConfig()
	if got != want {
		t.Errorf("EngineConfig() = %+v\nwant %+v", got, want)
	}
}

// TestEngineConfigKeepsExplicitValues 显式改过的字段必须原样带进搜索引擎配置。
func TestEngineConfigKeepsExplicitValues(t *testing.T) {
	cfg := BuildDefault(Config{}).Context.OnlineSearch
	cfg.ProxyURL = "socks5://127.0.0.1:1080"
	cfg.Bing.Enable = false
	cfg.Github.Enable = true
	cfg.Github.Token = "ghp_x"
	cfg.Github.MaxResults = 0
	cfg.DeepSeek.Enable = true
	cfg.DeepSeek.APIKey = "sk-x"
	cfg.DeepSeek.NeedContent = true
	cfg.DeepSeek.Localization = "1.2.3.4"
	cfg.Brave.ExtraSnippets = true

	got := cfg.EngineConfig()
	if got.ProxyURL != cfg.ProxyURL {
		t.Errorf("ProxyURL = %q, want %q", got.ProxyURL, cfg.ProxyURL)
	}
	if got.Bing.Enable {
		t.Error("Bing.Enable should be false")
	}
	if !got.Github.Enable || got.Github.Token != "ghp_x" || got.Github.MaxResults != 0 {
		t.Errorf("Github = %+v", got.Github)
	}
	if !got.DeepSeek.Enable || got.DeepSeek.APIKey != "sk-x" || !got.DeepSeek.NeedContent ||
		got.DeepSeek.Localization != "1.2.3.4" {
		t.Errorf("DeepSeek = %+v", got.DeepSeek)
	}
	if !got.Brave.ExtraSnippets {
		t.Error("Brave.ExtraSnippets should be true")
	}
}
