package actions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// ---- helpers ----

// configSetup 为测试创建临时配置环境。
// 设置 ALKAID0_CONFIG_PATH 到临时路径，保存 GlobalConfig 快照用于恢复。
func configSetup(t *testing.T) (restore func()) {
	t.Helper()

	// 保存内存中的配置快照
	origConfig := *config.GlobalConfig

	// 创建临时配置文件路径
	tmpDir := t.TempDir()
	tmpCfgPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("ALKAID0_CONFIG_PATH", tmpCfgPath)

	// 重置 configPath 缓存指向临时路径（Load 会从 env 重新解析），
	// 避免 config.Save() 误用其它测试缓存下来的旧路径。文件不存在时
	// Load 内部会创建默认配置并保存，后续 config.Save() 即可正常写入。
	config.Load()

	return func() {
		config.GlobalConfigSwap(origConfig)
	}
}

// ---- ConfigGet 测试 ----

// TestConfigGetReturnsNonNil config/get 应返回非 nil 的配置
func TestConfigGetReturnsNonNil(t *testing.T) {
	resp, err := ConfigGet(ConfigGetRequest{}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Config == nil {
		t.Fatal("ConfigGetResponse.Config should not be nil")
	}
}

// TestConfigGetReturnsSameGlobalConfig config/get 应返回与 GlobalConfig 相同指针
func TestConfigGetReturnsSameGlobalConfig(t *testing.T) {
	resp, err := ConfigGet(ConfigGetRequest{}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Config != config.GlobalConfig {
		t.Error("ConfigGet should return the same GlobalConfig pointer")
	}
}

// TestConfigGetJSONSerializable config/get 的响应应能正常 JSON 序列化/反序列化
func TestConfigGetJSONSerializable(t *testing.T) {
	resp, err := ConfigGet(ConfigGetRequest{}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("ConfigGetResponse should be JSON serializable: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("serialized config should not be empty")
	}

	var decoded ConfigGetResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("ConfigGetResponse should be JSON deserializable: %v", err)
	}
	if decoded.Config == nil {
		t.Fatal("decoded Config should not be nil")
	}
}

// ---- ConfigSet 测试 ----

// TestConfigSetNilConfig config/set 传入 nil config 应报错
func TestConfigSetNilConfig(t *testing.T) {
	_, err := ConfigSet(ConfigSetRequest{Config: nil}, nil, 0)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

// TestConfigSetInvalidJSON config/set 传入非法 JSON 应报错
func TestConfigSetInvalidJSON(t *testing.T) {
	_, err := ConfigSet(ConfigSetRequest{
		Config: json.RawMessage(`{invalid}`),
	}, nil, 0)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestConfigSetPartialUpdate 部分更新只影响指定字段，未指定字段保持不变
func TestConfigSetPartialUpdate(t *testing.T) {
	defer configSetup(t)()

	origHost := config.GlobalConfig.Server.Host

	newPort := uint16(19999)
	reqData, _ := json.Marshal(map[string]any{
		"Server": map[string]any{
			"port": newPort,
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Server.Port != newPort {
		t.Errorf("Server.Port = %d, want %d", config.GlobalConfig.Server.Port, newPort)
	}
	if config.GlobalConfig.Server.Host != origHost {
		t.Errorf("Server.Host should remain unchanged: got %q, want %q",
			config.GlobalConfig.Server.Host, origHost)
	}

	// 验证文件已持久化
	tmpCfgPath := os.Getenv("ALKAID0_CONFIG_PATH")
	savedData, err := os.ReadFile(tmpCfgPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}
	var savedCfg struct {
		Server struct {
			Port uint16 `json:"port"`
		} `json:"Server"`
	}
	if err := json.Unmarshal(savedData, &savedCfg); err != nil {
		t.Fatalf("failed to unmarshal saved config: %v", err)
	}
	if savedCfg.Server.Port != newPort {
		t.Errorf("saved config Server.Port = %d, want %d", savedCfg.Server.Port, newPort)
	}
}

// TestConfigSetFullUpdate 完整配置替换
func TestConfigSetFullUpdate(t *testing.T) {
	defer configSetup(t)()

	newHost := "0.0.0.0"
	newPort := uint16(19998)
	reqData, _ := json.Marshal(map[string]any{
		"Server": map[string]any{
			"host": newHost,
			"port": newPort,
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Server.Host != newHost {
		t.Errorf("Server.Host = %q, want %q", config.GlobalConfig.Server.Host, newHost)
	}
	if config.GlobalConfig.Server.Port != newPort {
		t.Errorf("Server.Port = %d, want %d", config.GlobalConfig.Server.Port, newPort)
	}
}

// TestConfigSetPreservesUnspecifiedField 未指定的嵌套字段应保持不变
func TestConfigSetPreservesUnspecifiedField(t *testing.T) {
	defer configSetup(t)()

	origPath := config.GlobalConfig.Server.Path

	reqData, _ := json.Marshal(map[string]any{
		"Server": map[string]any{
			"host": "config-set-test-host",
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Server.Path != origPath {
		t.Errorf("Server.Path should remain unchanged: got %q, want %q",
			config.GlobalConfig.Server.Path, origPath)
	}
}

// TestConfigSetEmptyJSONObject 空对象不应报错也不应修改任何字段
func TestConfigSetEmptyJSONObject(t *testing.T) {
	defer configSetup(t)()

	origHost := config.GlobalConfig.Server.Host

	reqData, _ := json.Marshal(map[string]any{})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error for empty object: %v", err)
	}

	if config.GlobalConfig.Server.Host != origHost {
		t.Error("empty JSON object should not modify any field")
	}
}

// TestConfigGetAfterSet config/set 后 config/get 应返回最新值
func TestConfigGetAfterSet(t *testing.T) {
	defer configSetup(t)()

	newPort := uint16(19997)
	reqData, _ := json.Marshal(map[string]any{
		"Server": map[string]any{
			"port": newPort,
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("ConfigSet failed: %v", err)
	}

	resp, err := ConfigGet(ConfigGetRequest{}, nil, 0)
	if err != nil {
		t.Fatalf("ConfigGet failed: %v", err)
	}
	if resp.Config.Server.Port != newPort {
		t.Errorf("after ConfigSet, Server.Port = %d, want %d",
			resp.Config.Server.Port, newPort)
	}
}

// TestConfigSetInvalidModelIDPrefix config/set 处理深层嵌套字段
func TestConfigSetUpdateModelConfig(t *testing.T) {
	defer configSetup(t)()

	// 更新 Model 中的 DefaultModelID
	newDefaultID := int32(42)
	reqData, _ := json.Marshal(map[string]any{
		"Model": map[string]any{
			"defaultModelID": newDefaultID,
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Model.DefaultModelID != newDefaultID {
		t.Errorf("Model.DefaultModelID = %d, want %d",
			config.GlobalConfig.Model.DefaultModelID, newDefaultID)
	}
}

// TestConfigSetDeleteModelItem config/set 传 {"Models":{"1":null}} 应真正删除模型键，
// 而不是像纯 json.Unmarshal 那样只把值置零（修复删除不同步到 server 的问题）。
func TestConfigSetDeleteModelItem(t *testing.T) {
	defer configSetup(t)()

	// 预置两个模型。
	config.GlobalConfig.Model.Models = map[int32]cfgStructs.ModelConfig{
		1: {ModelName: "one", ModelID: "m1"},
		2: {ModelName: "two", ModelID: "m2"},
	}
	reqData, _ := json.Marshal(map[string]any{
		"Model": map[string]any{
			"Models": map[string]any{"1": nil},
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m, ok := config.GlobalConfig.Model.Models[1]; ok {
		t.Errorf("model 1 should be deleted from GlobalConfig, got %+v", m)
	}
	if _, ok := config.GlobalConfig.Model.Models[2]; !ok {
		t.Error("model 2 should remain unchanged")
	}

	// 验证已持久化到文件（键 1 不应出现在保存的配置中）。
	tmpCfgPath := os.Getenv("ALKAID0_CONFIG_PATH")
	savedData, err := os.ReadFile(tmpCfgPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}
	var savedCfg struct {
		Model struct {
			Models map[string]json.RawMessage `json:"Models"`
		} `json:"Model"`
	}
	if err := json.Unmarshal(savedData, &savedCfg); err != nil {
		t.Fatalf("failed to unmarshal saved config: %v", err)
	}
	if _, ok := savedCfg.Model.Models["1"]; ok {
		t.Error("model 1 should not appear in the persisted config file")
	}
	if _, ok := savedCfg.Model.Models["2"]; !ok {
		t.Error("model 2 should appear in the persisted config file")
	}
}

// TestConfigSetDeleteAgentItem config/set 传 {"Agents":{"main":null}} 应真正删除子代理键。
func TestConfigSetDeleteAgentItem(t *testing.T) {
	defer configSetup(t)()

	config.GlobalConfig.Agent.Agents = map[string]cfgStructs.AgentConfig{
		"main":     {AgentName: "Main"},
		"frontend": {AgentName: "Front"},
	}
	reqData, _ := json.Marshal(map[string]any{
		"Agent": map[string]any{
			"Agents": map[string]any{"main": nil},
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a, ok := config.GlobalConfig.Agent.Agents["main"]; ok {
		t.Errorf("agent main should be deleted from GlobalConfig, got %+v", a)
	}
	if _, ok := config.GlobalConfig.Agent.Agents["frontend"]; !ok {
		t.Error("agent frontend should remain unchanged")
	}
}

// TestConfigSetNullStructField struct 字段传 null 保持 unmarshal 行为：
// encoding/json 对非 map 的 struct 字段传 null 不设置，保持原值不变。
func TestConfigSetNullStructField(t *testing.T) {
	defer configSetup(t)()

	config.GlobalConfig.Server.Host = "10.0.0.1"
	reqData, _ := json.Marshal(map[string]any{"Server": nil})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Server.Host != "10.0.0.1" {
		t.Errorf("Server.Host should remain unchanged for struct null, got %q", config.GlobalConfig.Server.Host)
	}
}
