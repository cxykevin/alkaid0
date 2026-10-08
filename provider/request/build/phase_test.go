package build

// phase 字段（GPT/Codex 系 API 的 commentary / final_answer）历史回放的开关断言。
// 纯本地断言，不需要真实 provider：只验证模型级 EnablePhase 打开/关闭时请求体里有没有该字段。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// enablePhase 在测试配置基础上打开/关闭模型级 EnablePhase。
func enablePhase(t *testing.T, enable bool) {
	t.Helper()
	setupTestConfig()
	cfg := *config.GlobalConfig
	models := make(map[int32]cfgStruct.ModelConfig, len(cfg.Model.Models))
	for k, v := range cfg.Model.Models {
		models[k] = v
	}
	model := models[cfg.Model.DefaultModelID]
	model.ProviderSpecificConfig.EnablePhase = enable
	models[cfg.Model.DefaultModelID] = model
	cfg.Model.Models = models
	config.GlobalConfigSwap(cfg)
}

// TestPhaseReplayGatedByModelSwitch 落库的 phase 只在模型级 EnablePhase 打开时随历史回放：
// 关闭（默认）时请求体里不得出现该字段，避免对不下发 phase 的供应商改变报文结构。
func TestPhaseReplayGatedByModelSwitch(t *testing.T) {
	db := setupTestDB(t)
	msgs := []structs.Messages{
		{ChatID: 61, Type: structs.MessagesRoleUser, Delta: "hello"},
		{ChatID: 61, Type: structs.MessagesRoleAgent, Delta: "working on it", Phase: "commentary"},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	render := func() string {
		req, err := RequestBody(61, 1, "", noTools(), db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
		if err != nil {
			t.Fatalf("RequestBody failed: %v", err)
		}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		return string(raw)
	}

	enablePhase(t, false)
	if body := render(); strings.Contains(body, `"phase"`) {
		t.Errorf("EnablePhase 关闭时不应回放 phase 字段：%s", body)
	}

	enablePhase(t, true)
	if body := render(); !strings.Contains(body, `"phase":"commentary"`) {
		t.Errorf("EnablePhase 开启时应原样回放落库的 phase：%s", body)
	}
}
