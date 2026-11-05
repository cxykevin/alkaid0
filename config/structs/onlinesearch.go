package structs

import (
	secconfig "github.com/cxykevin/alkaid0-search-engine/config"
)

// OnlineSearchConfig 在线搜索配置（Context.OnlineSearch）。
//
// 这里不用第三方包 secconfig.Config 直接当配置字段：它的字段全部带
// json:",omitempty"，Enable=false / 数值 0 / 空串在 JSON 里会直接消失，
// config/get 与配置文件里看不到这些键，客户端配置树也就没法开关搜索源。
// 因此在本项目内手写一份完整结构：每个 provider 的每个字段都显式声明、显式
// 写好默认值（default tag，由 BuildDefault 在加载时作为基底填充），JSON 一律
// 完整输出；调用搜索引擎前再用 EngineConfig 转回 secconfig.Config。
//
// 默认值口径：Bing 默认启用（无需密钥）且最多 10 条；GitHub / arXiv 每页 5 条；
// 其余 provider 默认关闭，非零默认与 alkaid0-search-engine 的 DefaultConfig 一致。
// 上游新增 provider / 字段时需要在这里补一份，并与 docs/schemas/config.json 同步。
type OnlineSearchConfig struct {
	// HTTP 基础配置
	Timeout    int    `json:"timeout" default:"30"`    // 请求超时（秒）
	ProxyURL   string `json:"proxy_url" default:""`    // HTTP 代理地址，支持 http/https/socks5
	RetryCount int    `json:"retry_count" default:"3"` // 重试次数

	Bing          BingSearchConfig          `json:"bing"`          // Bing 搜索
	Github        GithubSearchConfig        `json:"github"`        // GitHub 搜索
	Arxiv         ArxivSearchConfig         `json:"arxiv"`         // arXiv 搜索
	Tavily        TavilySearchConfig        `json:"tavily"`        // Tavily 搜索
	DeepSeek      DeepSeekSearchConfig      `json:"deepseek"`      // DeepSeek Search API
	Context7      Context7SearchConfig      `json:"context7"`      // Context7 库文档搜索
	Zread         ZreadSearchConfig         `json:"zread"`         // Zread 仓库搜索
	Brave         BraveSearchConfig         `json:"brave"`         // Brave Search
	Test          TestSearchConfig          `json:"test"`          // 测试 mock 搜索
	GrepApp       GrepAppSearchConfig       `json:"grepapp"`       // grep.app 代码搜索
	Sourcegraph   SourcegraphSearchConfig   `json:"sourcegraph"`   // Sourcegraph 代码搜索
	StackOverflow StackOverflowSearchConfig `json:"stackoverflow"` // Stack Overflow 问答搜索
	HackerNews    HackerNewsSearchConfig    `json:"hackernews"`    // Hacker News 技术讨论搜索
	Devto         DevtoSearchConfig         `json:"devto"`         // dev.to 技术文章搜索
	LibrariesIO   LibrariesIOSearchConfig   `json:"libraries_io"`  // libraries.io 依赖库搜索
}

// BingSearchConfig Bing 搜索配置（SERP 抓取，无需密钥）
type BingSearchConfig struct {
	Enable     bool   `json:"enable" default:"true"`
	UserAgent  string `json:"user_agent" default:"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"`
	ProxyURL   string `json:"proxy_url" default:""`
	MinDelay   int    `json:"min_delay" default:"2"`
	MaxDelay   int    `json:"max_delay" default:"5"`
	Market     string `json:"market" default:""`
	SetLang    string `json:"set_lang" default:""`
	Country    string `json:"country" default:""`
	SafeSearch string `json:"safe_search" default:""`
	Freshness  string `json:"freshness" default:""`
	MaxResults int    `json:"max_results" default:"10"`
}

// GithubSearchConfig GitHub 搜索配置（REST API v3）
type GithubSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	Token      string `json:"token" default:""`
	ProxyURL   string `json:"proxy_url" default:""`
	MaxResults int    `json:"max_results" default:"5"`
}

// ArxivSearchConfig arXiv 学术论文搜索配置（无需密钥）
type ArxivSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	ProxyURL   string `json:"proxy_url" default:""`
	MaxResults int    `json:"max_results" default:"5"`
}

// TavilySearchConfig Tavily 通用 web 搜索配置（需 API Key）
type TavilySearchConfig struct {
	Enable        bool   `json:"enable" default:"false"`
	APIKey        string `json:"api_key" default:""`
	ProxyURL      string `json:"proxy_url" default:""`
	SearchDepth   string `json:"search_depth" default:"basic"`
	MaxResults    int    `json:"max_results" default:"10"`
	IncludeAnswer bool   `json:"include_answer" default:"false"`
	IncludeRaw    bool   `json:"include_raw_content" default:"false"`
}

// DeepSeekSearchConfig DeepSeek Search API 配置（需 API Key）
// API 参考：https://api-docs.deepseek.com/api/search
type DeepSeekSearchConfig struct {
	Enable       bool   `json:"enable" default:"false"`
	APIKey       string `json:"api_key" default:""`
	ProxyURL     string `json:"proxy_url" default:""`
	MaxResults   int    `json:"max_results" default:"10"`
	NeedContent  bool   `json:"need_content" default:"false"`
	Localization string `json:"localization" default:""`
}

// Context7SearchConfig Context7 库文档搜索配置（API Key 选填）
type Context7SearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	APIKey     string `json:"api_key" default:""`
	ProxyURL   string `json:"proxy_url" default:""`
	Fast       bool   `json:"fast" default:"false"`
	MaxResults int    `json:"max_results" default:"0"`
}

// ZreadSearchConfig Zread 仓库搜索配置
type ZreadSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	ProxyURL   string `json:"proxy_url" default:""`
	Locale     string `json:"locale" default:"zh"`
	UserAgent  string `json:"user_agent" default:"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"`
	MinDelay   int    `json:"min_delay" default:"2"`
	MaxDelay   int    `json:"max_delay" default:"5"`
	MaxResults int    `json:"max_results" default:"0"`
}

// BraveSearchConfig Brave Search 配置（需订阅令牌）
type BraveSearchConfig struct {
	Enable        bool   `json:"enable" default:"false"`
	APIKey        string `json:"api_key" default:""`
	ProxyURL      string `json:"proxy_url" default:""`
	Country       string `json:"country" default:"US"`
	SearchLang    string `json:"search_lang" default:"en"`
	UILang        string `json:"ui_lang" default:"en-US"`
	Safesearch    string `json:"safesearch" default:"moderate"`
	Freshness     string `json:"freshness" default:""`
	ResultFilter  string `json:"result_filter" default:""`
	MaxResults    int    `json:"max_results" default:"10"`
	ExtraSnippets bool   `json:"extra_snippets" default:"false"`
}

// TestSearchConfig 测试 mock 搜索配置（不依赖外部服务）
type TestSearchConfig struct {
	Enable     bool `json:"enable" default:"false"`
	MaxResults int  `json:"max_results" default:"0"`
}

// GrepAppSearchConfig grep.app 代码搜索配置
type GrepAppSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	ProxyURL   string `json:"proxy_url" default:""`
	MaxResults int    `json:"max_results" default:"0"`
}

// SourcegraphSearchConfig Sourcegraph 代码搜索配置
type SourcegraphSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	Token      string `json:"token" default:""`
	ProxyURL   string `json:"proxy_url" default:""`
	MaxResults int    `json:"max_results" default:"0"`
}

// StackOverflowSearchConfig Stack Overflow 问答搜索配置
type StackOverflowSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	APIKey     string `json:"api_key" default:""`
	ProxyURL   string `json:"proxy_url" default:""`
	MaxResults int    `json:"max_results" default:"10"`
}

// HackerNewsSearchConfig Hacker News 技术讨论搜索配置
type HackerNewsSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	ProxyURL   string `json:"proxy_url" default:""`
	MaxResults int    `json:"max_results" default:"10"`
}

// DevtoSearchConfig dev.to 技术文章搜索配置
type DevtoSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	APIKey     string `json:"api_key" default:""`
	ProxyURL   string `json:"proxy_url" default:""`
	Tag        string `json:"tag" default:""`
	MaxResults int    `json:"max_results" default:"10"`
}

// LibrariesIOSearchConfig libraries.io 依赖库搜索配置
type LibrariesIOSearchConfig struct {
	Enable     bool   `json:"enable" default:"false"`
	APIKey     string `json:"api_key" default:""`
	ProxyURL   string `json:"proxy_url" default:""`
	MaxResults int    `json:"max_results" default:"10"`
}

// EngineConfig 转换成 alkaid0-search-engine 使用的配置结构。
func (c OnlineSearchConfig) EngineConfig() secconfig.Config {
	return secconfig.Config{
		Timeout:    c.Timeout,
		ProxyURL:   c.ProxyURL,
		RetryCount: c.RetryCount,
		Bing: secconfig.BingConfig{
			Enable:     c.Bing.Enable,
			UserAgent:  c.Bing.UserAgent,
			ProxyURL:   c.Bing.ProxyURL,
			MinDelay:   c.Bing.MinDelay,
			MaxDelay:   c.Bing.MaxDelay,
			Market:     c.Bing.Market,
			SetLang:    c.Bing.SetLang,
			Country:    c.Bing.Country,
			SafeSearch: c.Bing.SafeSearch,
			Freshness:  c.Bing.Freshness,
			MaxResults: c.Bing.MaxResults,
		},
		Github: secconfig.GithubConfig{
			Enable:     c.Github.Enable,
			Token:      c.Github.Token,
			ProxyURL:   c.Github.ProxyURL,
			MaxResults: c.Github.MaxResults,
		},
		Arxiv: secconfig.ArxivConfig{
			Enable:     c.Arxiv.Enable,
			ProxyURL:   c.Arxiv.ProxyURL,
			MaxResults: c.Arxiv.MaxResults,
		},
		Tavily: secconfig.TavilyConfig{
			Enable:        c.Tavily.Enable,
			APIKey:        c.Tavily.APIKey,
			ProxyURL:      c.Tavily.ProxyURL,
			SearchDepth:   c.Tavily.SearchDepth,
			MaxResults:    c.Tavily.MaxResults,
			IncludeAnswer: c.Tavily.IncludeAnswer,
			IncludeRaw:    c.Tavily.IncludeRaw,
		},
		DeepSeek: secconfig.DeepSeekConfig{
			Enable:       c.DeepSeek.Enable,
			APIKey:       c.DeepSeek.APIKey,
			ProxyURL:     c.DeepSeek.ProxyURL,
			MaxResults:   c.DeepSeek.MaxResults,
			NeedContent:  c.DeepSeek.NeedContent,
			Localization: c.DeepSeek.Localization,
		},
		Context7: secconfig.Context7Config{
			Enable:     c.Context7.Enable,
			APIKey:     c.Context7.APIKey,
			ProxyURL:   c.Context7.ProxyURL,
			Fast:       c.Context7.Fast,
			MaxResults: c.Context7.MaxResults,
		},
		Zread: secconfig.ZreadConfig{
			Enable:     c.Zread.Enable,
			ProxyURL:   c.Zread.ProxyURL,
			Locale:     c.Zread.Locale,
			UserAgent:  c.Zread.UserAgent,
			MinDelay:   c.Zread.MinDelay,
			MaxDelay:   c.Zread.MaxDelay,
			MaxResults: c.Zread.MaxResults,
		},
		Brave: secconfig.BraveConfig{
			Enable:        c.Brave.Enable,
			APIKey:        c.Brave.APIKey,
			ProxyURL:      c.Brave.ProxyURL,
			Country:       c.Brave.Country,
			SearchLang:    c.Brave.SearchLang,
			UILang:        c.Brave.UILang,
			Safesearch:    c.Brave.Safesearch,
			Freshness:     c.Brave.Freshness,
			ResultFilter:  c.Brave.ResultFilter,
			MaxResults:    c.Brave.MaxResults,
			ExtraSnippets: c.Brave.ExtraSnippets,
		},
		Test: secconfig.TestConfig{
			Enable:     c.Test.Enable,
			MaxResults: c.Test.MaxResults,
		},
		GrepApp: secconfig.GrepAppConfig{
			Enable:     c.GrepApp.Enable,
			ProxyURL:   c.GrepApp.ProxyURL,
			MaxResults: c.GrepApp.MaxResults,
		},
		Sourcegraph: secconfig.SourcegraphConfig{
			Enable:     c.Sourcegraph.Enable,
			Token:      c.Sourcegraph.Token,
			ProxyURL:   c.Sourcegraph.ProxyURL,
			MaxResults: c.Sourcegraph.MaxResults,
		},
		StackOverflow: secconfig.StackOverflowConfig{
			Enable:     c.StackOverflow.Enable,
			APIKey:     c.StackOverflow.APIKey,
			ProxyURL:   c.StackOverflow.ProxyURL,
			MaxResults: c.StackOverflow.MaxResults,
		},
		HackerNews: secconfig.HackerNewsConfig{
			Enable:     c.HackerNews.Enable,
			ProxyURL:   c.HackerNews.ProxyURL,
			MaxResults: c.HackerNews.MaxResults,
		},
		Devto: secconfig.DevtoConfig{
			Enable:     c.Devto.Enable,
			APIKey:     c.Devto.APIKey,
			ProxyURL:   c.Devto.ProxyURL,
			Tag:        c.Devto.Tag,
			MaxResults: c.Devto.MaxResults,
		},
		LibrariesIO: secconfig.LibrariesIOConfig{
			Enable:     c.LibrariesIO.Enable,
			APIKey:     c.LibrariesIO.APIKey,
			ProxyURL:   c.LibrariesIO.ProxyURL,
			MaxResults: c.LibrariesIO.MaxResults,
		},
	}
}
