package actions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/config"
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

	// 保存一份初始默认配置到临时路径（使后续 config.Save() 能正常写入）
	// config.Save() 内部用 configPath 缓存，首次调用时从 env 读取路径
	config.Save()

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
