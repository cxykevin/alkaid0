package config

import (
	"encoding/json"

	secconfig "github.com/cxykevin/alkaid0-search-engine/config"

	"github.com/cxykevin/alkaid0/config/structs"
)

// defaultOnlineSearch 返回 Context.OnlineSearch 的期望默认值。
//
// OnlineSearch 来自第三方包 alkaid0-search-engine（secconfig.Config），其字段只有
// json tag、没有 default tag，BuildDefault 递归进入后不会填充任何默认值——全新安装
// 或配置缺失时 config/get 返回全零（Bing.Enable=false 等），且 search.Search 仅在
// cfg==nil 时套用 secconfig.DefaultConfig()，非 nil 的全零配置同样一个搜索源都不
// 启用（在线搜索直接报 "no search sources enabled"）。因此这里定义产品期望的默认
// 值：Bing 默认启用（无需密钥），GitHub/arXiv/Tavily 默认关闭。
func defaultOnlineSearch() secconfig.Config {
	return secconfig.Config{
		Timeout:    30,
		RetryCount: 3,
		Bing: secconfig.BingConfig{
			Enable:     true,
			MinDelay:   2,
			MaxDelay:   5,
			MaxResults: 10,
		},
		Github: secconfig.GithubConfig{
			Enable:     false,
			MaxResults: 5,
		},
		Arxiv: secconfig.ArxivConfig{
			Enable:     false,
			MaxResults: 5,
		},
		Tavily: secconfig.TavilyConfig{
			Enable:      false,
			SearchDepth: "basic",
			MaxResults:  10,
		},
	}
}

// EnsureOnlineSearchDefaults 填充 Context.OnlineSearch 缺失字段为期望默认值。
// raw 是加载的完整配置 JSON（可为 nil/空，表示无既有配置，全部套用默认值）。
//
// 为什么不直接按 Go 零值判断"未配置"：Enable 的默认值有 true（Bing）与 false
// （其余），而 false 本身就是零值，无法与"用户显式写了 false"区分——若按零值
// 填充会把用户显式关闭的 Bing 又改回 true。因此改为探测 raw 中实际出现过的
// 键：某字段未出现才填默认，显式写过的值（含 false）一律保留。某 provider 整个
// 未出现时整体套用默认值（含 Enable 开关）。
//
// 在配置加载（config.Load）后调用；config/set 写回不调用（编辑以用户值为准）。
func EnsureOnlineSearchDefaults(cfg *structs.Config, raw json.RawMessage) {
	if cfg == nil {
		return
	}
	d := defaultOnlineSearch()
	seen := onlineSearchSeen(raw)
	on := cfg.Context.OnlineSearch
	if _, present := seen["timeout"]; !present {
		on.Timeout = d.Timeout
	}
	if _, present := seen["retry_count"]; !present {
		on.RetryCount = d.RetryCount
	}
	fillBing(&on.Bing, d.Bing, seen["bing"])
	fillGithub(&on.Github, d.Github, seen["github"])
	fillArxiv(&on.Arxiv, d.Arxiv, seen["arxiv"])
	fillTavily(&on.Tavily, d.Tavily, seen["tavily"])
	cfg.Context.OnlineSearch = on
}

// onlineSearchSeen 解析原始配置 JSON，返回 Context.OnlineSearch 中实际出现过的键。
// 值为该字段的子键集合：provider 出现则为其内部出现的键名集合，标量字段出现则为
// 空 map（非 nil）；整个 OnlineSearch 或某字段未出现时对应键缺失（nil）。
func onlineSearchSeen(raw json.RawMessage) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if len(raw) == 0 {
		return out
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return out
	}
	ctxRaw, ok := top["Context"]
	if !ok {
		return out
	}
	var ctx map[string]json.RawMessage
	if err := json.Unmarshal(ctxRaw, &ctx); err != nil {
		return out
	}
	onRaw, ok := ctx["OnlineSearch"]
	if !ok {
		return out
	}
	var on map[string]json.RawMessage
	if err := json.Unmarshal(onRaw, &on); err != nil {
		return out
	}
	for k, v := range on {
		if len(v) == 0 || string(v) == "null" {
			out[k] = map[string]bool{}
			continue
		}
		var sub map[string]json.RawMessage
		if err := json.Unmarshal(v, &sub); err == nil {
			m := make(map[string]bool, len(sub))
			for sk := range sub {
				m[sk] = true
			}
			out[k] = m
			continue
		}
		out[k] = nil
	}
	return out
}

// fillBing 填充 Bing 缺失字段：bing 键整体未出现时套用默认值（含 Enable=true）；
// 否则仅填充未显式写过的字段，用户显式的 Enable（含 false）保留。
func fillBing(dst *secconfig.BingConfig, d secconfig.BingConfig, seen map[string]bool) {
	if len(seen) == 0 {
		*dst = d
		return
	}
	if !seen["enable"] {
		dst.Enable = d.Enable
	}
	if !seen["min_delay"] {
		dst.MinDelay = d.MinDelay
	}
	if !seen["max_delay"] {
		dst.MaxDelay = d.MaxDelay
	}
	if !seen["max_results"] {
		dst.MaxResults = d.MaxResults
	}
}

// fillGithub 填充 GitHub 缺失字段（默认 Enable=false，与零值一致；仅补 MaxResults）。
func fillGithub(dst *secconfig.GithubConfig, d secconfig.GithubConfig, seen map[string]bool) {
	if len(seen) == 0 {
		*dst = d
		return
	}
	if !seen["max_results"] {
		dst.MaxResults = d.MaxResults
	}
}

// fillArxiv 填充 arXiv 缺失字段（默认 Enable=false，仅补 MaxResults）。
func fillArxiv(dst *secconfig.ArxivConfig, d secconfig.ArxivConfig, seen map[string]bool) {
	if len(seen) == 0 {
		*dst = d
		return
	}
	if !seen["max_results"] {
		dst.MaxResults = d.MaxResults
	}
}

// fillTavily 填充 Tavily 缺失字段（默认 Enable=false，补 SearchDepth 与 MaxResults）。
func fillTavily(dst *secconfig.TavilyConfig, d secconfig.TavilyConfig, seen map[string]bool) {
	if len(seen) == 0 {
		*dst = d
		return
	}
	if !seen["search_depth"] {
		dst.SearchDepth = d.SearchDepth
	}
	if !seen["max_results"] {
		dst.MaxResults = d.MaxResults
	}
}
