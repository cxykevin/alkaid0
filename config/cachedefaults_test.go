package config

import (
	"encoding/json"
	"testing"

	"github.com/cxykevin/alkaid0/config/structs"
)

func TestEnsureCacheDefaults(t *testing.T) {
	// 存量配置缺新字段 → 填默认
	raw := json.RawMessage(`{"Model":{"Models":{"1":{"ModelName":"m"}}}}`)
	cfg := &structs.Config{}
	cfg.Model.Models = map[int32]structs.ModelConfig{1: {ModelName: "m"}}
	EnsureCacheDefaults(cfg, raw)

	if got := cfg.Model.Models[1].CachePriceMultiplier; got != 0.2 {
		t.Errorf("CachePriceMultiplier default = %v, want 0.2", got)
	}
	if got := cfg.Model.Models[1].CacheRetentionMinutes; got != 180 {
		t.Errorf("CacheRetentionMinutes default = %v, want 180", got)
	}

	// 显式写 0 的字段保留（不覆盖为默认）
	raw2 := json.RawMessage(`{"Model":{"Models":{"1":{"CachePriceMultiplier":0}}}}`)
	cfg2 := &structs.Config{}
	cfg2.Model.Models = map[int32]structs.ModelConfig{1: {ModelName: "m"}}
	EnsureCacheDefaults(cfg2, raw2)

	if got := cfg2.Model.Models[1].CachePriceMultiplier; got != 0 {
		t.Errorf("explicit 0 should be kept, got %v", got)
	}
	if got := cfg2.Model.Models[1].CacheRetentionMinutes; got != 180 {
		t.Errorf("CacheRetentionMinutes default = %v, want 180", got)
	}

	// 无 raw（全新配置）→ 整体套默认
	cfg3 := &structs.Config{}
	cfg3.Model.Models = map[int32]structs.ModelConfig{1: {ModelName: "m"}}
	EnsureCacheDefaults(cfg3, nil)
	if got := cfg3.Model.Models[1].CachePriceMultiplier; got != 0.2 {
		t.Errorf("nil raw multiplier = %v, want 0.2", got)
	}
}
