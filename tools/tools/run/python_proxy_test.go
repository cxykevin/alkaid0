package run

import (
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/server/apikey"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
)

// TestProxyKeyTTLIs24Hours 维护者决定：代理 key 不做续期，每次代码运行现场分配，
// 有效期统一 24 小时。旧实现前台固定 30 分钟、后台按超时放宽到最多 7 天，
// 长任务跑到一半就 401（运行中的进程拿不到新 key，也没有续期接口）。
func TestProxyKeyTTLIs24Hours(t *testing.T) {
	if proxyKeyTTLMinutes != 24*60 {
		t.Fatalf("代理 key 有效期 = %d 分钟，期望 24 小时（%d）", proxyKeyTTLMinutes, 24*60)
	}
}

// TestProxyKeyAllocatedFreshPerRun 每次调用 buildProxyEnv（即每次代码运行）都必须
// 分配到不同的、可立即使用的 key；任务结束由 cleanupFn 删除。
func TestProxyKeyAllocatedFreshPerRun(t *testing.T) {
	restore := config.GlobalConfigSwap(cfgStruct.Config{
		Server: cfgStruct.RPCConfig{Host: "127.0.0.1", Port: 7433},
		Model: cfgStruct.ModelsConfig{
			Models: map[int32]cfgStruct.ModelConfig{1: {ModelID: "proxy-test-model"}},
		},
	})
	defer restore()

	session := &storageStructs.Chats{ID: 1, LastModelID: 1}

	k1, url1, model1, err := buildProxyEnv(session, proxyKeyTTLMinutes)
	if err != nil {
		t.Fatalf("buildProxyEnv #1: %v", err)
	}
	k2, _, _, err := buildProxyEnv(session, proxyKeyTTLMinutes)
	if err != nil {
		t.Fatalf("buildProxyEnv #2: %v", err)
	}

	if k1 == "" || k2 == "" {
		t.Fatal("代理 key 不应为空")
	}
	if k1 == k2 {
		t.Error("每次代码运行必须分配新的代理 key")
	}
	if !apikey.Validate(k1) || !apikey.Validate(k2) {
		t.Error("新分配的代理 key 必须立即可用")
	}
	if model1 != "proxy-test-model" {
		t.Errorf("modelID = %q, want proxy-test-model", model1)
	}
	if url1 == "" {
		t.Error("baseURL 不应为空")
	}
	// 清理，避免影响同包其它用例
	apikey.Delete(k1)
	apikey.Delete(k2)
}
