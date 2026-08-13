package config

import (
	"encoding/json"
	"strconv"

	"github.com/cxykevin/alkaid0/config/structs"
)

// 缓存相关配置的期望默认值。与 default tag 保持一致，用于存量配置文件
// （缺新字段时 unmarshal 会得到零值，而非 default tag 值）的补齐。
const (
	defaultCachePriceMultiplier  float32 = 0.2  // 缓存命中 token 相对输入价格的倍率
	defaultCacheRetentionMinutes int32   = 180  // 缓存保留时间（分钟）
)

// EnsureCacheDefaults 填充 Model.Models 各模型缺失的缓存字段为期望默认值。
// raw 是加载的完整配置 JSON（可为 nil/空，表示无既有配置）。
//
// 与 EnsureOnlineSearchDefaults 同理：Load() 在 unmarshal 成功后用
// *GlobalConfig = *tempConfig 整体替换，会导致存量配置缺失的新字段变零值
// （0.0 / 0），而非 default tag 的默认值。因此探测 raw 中各模型实际写过的键，
// 未写的字段才填默认，用户显式写过的值（含 0）一律保留。
func EnsureCacheDefaults(cfg *structs.Config, raw json.RawMessage) {
	if cfg == nil {
		return
	}
	seen := cacheSeen(raw)
	for id, m := range cfg.Model.Models {
		key := strconv.FormatInt(int64(id), 10)
		fields, present := seen[key]
		if present {
			// 模型在 raw 中出现过：只补未显式写过的字段。
			if !fields["CachePriceMultiplier"] {
				m.CachePriceMultiplier = defaultCachePriceMultiplier
			}
			if !fields["CacheRetentionMinutes"] {
				m.CacheRetentionMinutes = defaultCacheRetentionMinutes
			}
		} else {
			// 模型未在 raw 中出现（理论不会发生，来自 raw 的 map）：整体套默认。
			m.CachePriceMultiplier = defaultCachePriceMultiplier
			m.CacheRetentionMinutes = defaultCacheRetentionMinutes
		}
		cfg.Model.Models[id] = m
	}
}

// cacheSeen 解析原始配置 JSON，返回 Model.Models 各模型（键为整数字符串）实际出现过的字段键。
func cacheSeen(raw json.RawMessage) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if len(raw) == 0 {
		return out
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return out
	}
	modelRaw, ok := top["Model"]
	if !ok {
		return out
	}
	var model map[string]json.RawMessage
	if err := json.Unmarshal(modelRaw, &model); err != nil {
		return out
	}
	modelsRaw, ok := model["Models"]
	if !ok {
		return out
	}
	var models map[string]json.RawMessage
	if err := json.Unmarshal(modelsRaw, &models); err != nil {
		return out
	}
	for id, v := range models {
		if len(v) == 0 || string(v) == "null" {
			out[id] = map[string]bool{}
			continue
		}
		var sub map[string]json.RawMessage
		if err := json.Unmarshal(v, &sub); err != nil {
			continue
		}
		m := make(map[string]bool, len(sub))
		for sk := range sub {
			m[sk] = true
		}
		out[id] = m
	}
	return out
}
