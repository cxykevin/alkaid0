package actions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// TestBuildConfigOptionsHideFilter 验证 buildConfigOptions 的 options 过滤隐藏模型
func TestBuildConfigOptionsHideFilter(t *testing.T) {
	defer configSetup(t)()

	*config.GlobalConfig = cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			DefaultModelID: 0,
			Models: map[int32]cfgStructs.ModelConfig{
				0: {ModelName: "Visible A", ModelID: "vis-a", Hide: false},
				1: {ModelName: "Hidden B", ModelID: "hid-b", Hide: true},
				2: {ModelName: "Visible C", ModelID: "vis-c", Hide: false},
			},
		},
	}

	options := buildConfigOptions(0, "unset")
	var modelOpt *ConfigOption
	for i := range options {
		if options[i].ConfigID == "model" {
			modelOpt = &options[i]
			break
		}
	}
	if modelOpt == nil {
		t.Fatal("model config option not found")
	}

	var got []string
	for _, o := range modelOpt.Options {
		got = append(got, o.Value)
	}
	want := []string{"0/vis-a", "2/vis-c"}
	if len(got) != len(want) {
		t.Fatalf("Hide 过滤失败: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Hide 过滤失败: got %v, want %v", got, want)
		}
	}
}

// TestBuildConfigOptionsCurrentValueFallback 验证当前模型被 Hide/不存在时 currentValue 回退到第一个可见模型
func TestBuildConfigOptionsCurrentValueFallback(t *testing.T) {
	defer configSetup(t)()

	*config.GlobalConfig = cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			DefaultModelID: 1,
			Models: map[int32]cfgStructs.ModelConfig{
				0: {ModelName: "Visible A", ModelID: "vis-a", Hide: false},
				1: {ModelName: "Hidden B", ModelID: "hid-b", Hide: true},
			},
		},
	}

	// 当前模型 1 被隐藏：currentValue 应回退到 0/vis-a
	options := buildConfigOptions(1, "unset")
	var modelOpt *ConfigOption
	for i := range options {
		if options[i].ConfigID == "model" {
			modelOpt = &options[i]
			break
		}
	}
	if modelOpt == nil {
		t.Fatal("model config option not found")
	}
	if modelOpt.CurrentValue != "0/vis-a" {
		t.Errorf("currentValue 应回退到可见模型, got %q, want %q", modelOpt.CurrentValue, "0/vis-a")
	}
}

// TestBuildConfigOptionsCurrentValueKeepsVisible 验证当前模型可见时 currentValue 保持不变
func TestBuildConfigOptionsCurrentValueKeepsVisible(t *testing.T) {
	defer configSetup(t)()

	*config.GlobalConfig = cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			DefaultModelID: 1,
			Models: map[int32]cfgStructs.ModelConfig{
				0: {ModelName: "Hidden", ModelID: "hid", Hide: true},
				1: {ModelName: "Visible B", ModelID: "vis-b", Hide: false},
			},
		},
	}

	options := buildConfigOptions(1, "unset")
	var modelOpt *ConfigOption
	for i := range options {
		if options[i].ConfigID == "model" {
			modelOpt = &options[i]
			break
		}
	}
	if modelOpt == nil {
		t.Fatal("model config option not found")
	}
	if modelOpt.CurrentValue != "1/vis-b" {
		t.Errorf("可见模型 currentValue 不应改变, got %q, want %q", modelOpt.CurrentValue, "1/vis-b")
	}
}

// TestResolveNewSessionModel 验证新会话模型解析逻辑：
// 优先 LastModelID（可见），否则默认模型（可见），否则键最小的可见模型。
func TestResolveNewSessionModel(t *testing.T) {
	defer configSetup(t)()

	setModels := func(defaultID int32, models map[int32]cfgStructs.ModelConfig) {
		*config.GlobalConfig = cfgStructs.Config{
			Model: cfgStructs.ModelsConfig{DefaultModelID: defaultID, Models: models},
		}
	}

	t.Run("LastModelID 可见则保留", func(t *testing.T) {
		setModels(2, map[int32]cfgStructs.ModelConfig{
			1: {ModelID: "m1", Hide: false},
			2: {ModelID: "m2", Hide: false},
			3: {ModelID: "m3", Hide: true},
		})
		if got := resolveNewSessionModel(1); got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("LastModelID 隐藏则回退默认模型", func(t *testing.T) {
		setModels(2, map[int32]cfgStructs.ModelConfig{
			1: {ModelID: "m1", Hide: true},
			2: {ModelID: "m2", Hide: false},
		})
		if got := resolveNewSessionModel(1); got != 2 {
			t.Errorf("got %d, want 2", got)
		}
	})

	t.Run("LastModelID 为0使用默认模型", func(t *testing.T) {
		setModels(2, map[int32]cfgStructs.ModelConfig{
			0: {ModelID: "m0", Hide: false},
			2: {ModelID: "m2", Hide: false},
		})
		if got := resolveNewSessionModel(0); got != 2 {
			t.Errorf("got %d, want 2（默认模型优先于模型0）", got)
		}
	})

	t.Run("默认模型隐藏则回退第一个可见模型", func(t *testing.T) {
		setModels(2, map[int32]cfgStructs.ModelConfig{
			1: {ModelID: "m1", Hide: false},
			2: {ModelID: "m2", Hide: true},
		})
		if got := resolveNewSessionModel(0); got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("全部隐藏返回0", func(t *testing.T) {
		setModels(2, map[int32]cfgStructs.ModelConfig{
			2: {ModelID: "m2", Hide: true},
		})
		if got := resolveNewSessionModel(0); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

// TestModelConfigHideJSONParsing 验证 Hide 字段的 JSON 解析（大写/小写均支持）
func TestModelConfigHideJSONParsing(t *testing.T) {
	for _, raw := range []string{`{"Hide": true}`, `{"hide": true}`} {
		var mc cfgStructs.ModelConfig
		if err := json.Unmarshal([]byte(raw), &mc); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if !mc.Hide {
			t.Errorf("JSON %s 未解析出 Hide=true", raw)
		}
	}
}

// TestSessionSetConfigOptionRejectsHiddenModel 验证 session/set_config_option 拒绝选中隐藏模型
func TestSessionSetConfigOptionRejectsHiddenModel(t *testing.T) {
	defer configSetup(t)()

	*config.GlobalConfig = cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			DefaultModelID: 0,
			Models: map[int32]cfgStructs.ModelConfig{
				0: {ModelName: "Visible", ModelID: "vis", Hide: false},
				1: {ModelName: "Hidden", ModelID: "hid", Hide: true},
			},
		},
	}

	dir, db, ids := newSessionListDB(t, 1)
	sessionID := cwd2SessionID(dir, ids[0])
	sessObj := &structs.Chats{ID: ids[0], DB: db}
	sessLock.Lock()
	sessions[sessionID] = &sessionObj{cwd: dir, id: ids[0], session: sessObj}
	sessLock.Unlock()
	t.Cleanup(func() {
		sessLock.Lock()
		delete(sessions, sessionID)
		sessLock.Unlock()
	})

	// 设置隐藏模型应被拒绝
	_, err := SessionSetConfigOption(SessionSetConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "model",
		Value:     "1/hid",
	}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("设置隐藏模型应被拒绝, got %v", err)
	}

	// 设置可见模型应成功
	if _, err := SessionSetConfigOption(SessionSetConfigOptionRequest{
		SessionID: sessionID,
		ConfigID:  "model",
		Value:     "0/vis",
	}, nil, 1); err != nil {
		t.Fatalf("设置可见模型失败: %v", err)
	}
}
