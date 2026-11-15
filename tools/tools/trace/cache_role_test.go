package trace

import (
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// TestCacheModelConfigUsesEffectiveModel 验证缓存倍率/保留期按 EffectiveModelID 取：
// 活跃子代理 > 会话 role > LastModelID。
func TestCacheModelConfigUsesEffectiveModel(t *testing.T) {
	restore := config.GlobalConfigSwap(cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			Models: map[int32]cfgStructs.ModelConfig{
				1: {CachePriceMultiplier: 0.5, CacheRetentionMinutes: 60},
				2: {CachePriceMultiplier: 0.02, CacheRetentionMinutes: 120},
			},
		},
	})
	defer restore()

	// 无 role / 无子代理：使用 LastModelID
	sess := &structs.Chats{LastModelID: 1}
	if mult, ret := cacheModelConfig(sess); mult != 0.5 || ret != 60 {
		t.Errorf("LastModelID 模型: mult=%v ret=%d, want 0.5/60", mult, ret)
	}

	// role 覆盖模型
	sess.SetRoleRuntime("cheap", cfgStructs.AgentConfig{AgentModel: 2})
	if mult, ret := cacheModelConfig(sess); mult != 0.02 || ret != 120 {
		t.Errorf("role 模型: mult=%v ret=%d, want 0.02/120", mult, ret)
	}

	// 活跃子代理优先于 role
	sess.NowAgent = "sub"
	sess.CurrentAgentConfig = cfgStructs.AgentConfig{AgentModel: 1}
	if mult, ret := cacheModelConfig(sess); mult != 0.5 || ret != 60 {
		t.Errorf("子代理模型: mult=%v ret=%d, want 0.5/60", mult, ret)
	}

	// role 不覆盖（AgentModel=0，跟随会话模型）
	sess.NowAgent = ""
	sess.CurrentAgentConfig = cfgStructs.AgentConfig{}
	sess.SetRoleRuntime("follow", cfgStructs.AgentConfig{})
	if mult, ret := cacheModelConfig(sess); mult != 0.5 || ret != 60 {
		t.Errorf("role 跟随会话模型: mult=%v ret=%d, want 0.5/60", mult, ret)
	}
}
