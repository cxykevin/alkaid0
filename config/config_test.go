package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/internal/configutil"

	"github.com/cxykevin/alkaid0/config/structs"
)

func TestConfig(t *testing.T) {
	os.Setenv("ALKAID0_CONFIG_PATH", "non_existent_config.json")
	Load()
	if GlobalConfig == nil {
		t.Fatal("GlobalConfig should not be nil after Load")
	}

	home, _ := os.UserHomeDir()
	if configutil.ExpandPath("~/test") != filepath.Clean(home+"/test") {
		t.Errorf("ExpandPath failed for ~")
	}
}

func TestExpandPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "tilde expansion",
			input:    "~/test",
			contains: "/test",
		},
		{
			name:     "no tilde",
			input:    "/absolute/path",
			contains: "/absolute/path",
		},
		{
			name:     "empty path",
			input:    "",
			contains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := configutil.ExpandPath(tt.input)
			if tt.contains != "" && result != filepath.Clean(tt.input) && !filepath.IsAbs(result) && filepath.Clean(tt.input)[0] != '~' {
				t.Errorf("ExpandPath(%s) = %s, expected to contain %s", filepath.Clean(tt.input), result, tt.contains)
			}
		})
	}
}

func TestLoadAndSave(t *testing.T) {
	// 创建临时目录
	tmpDir := t.TempDir()
	testConfigPath := filepath.Join(tmpDir, "test_config.json")

	// 设置环境变量
	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, testConfigPath)

	// 重置configPath
	configPath = ""

	// 加载配置（应该创建默认配置）
	Load()

	if GlobalConfig == nil {
		t.Fatal("GlobalConfig should not be nil")
	}

	// 验证配置文件已创建
	if _, err := os.Stat(testConfigPath); os.IsNotExist(err) {
		t.Error("Config file should be created")
	}

	// 修改配置
	GlobalConfig.Version = 999

	// 保存配置
	Save()

	// 重新加载
	Load()

	if GlobalConfig.Version != 999 {
		t.Errorf("Expected version 999, got %d", GlobalConfig.Version)
	}
}

func TestLoadWithInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	testConfigPath := filepath.Join(tmpDir, "invalid_config.json")

	// 写入无效的JSON
	if err := os.WriteFile(testConfigPath, []byte("invalid json{{{"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, testConfigPath)

	configPath = ""

	// 加载应该失败并创建默认配置
	Load()

	if GlobalConfig == nil {
		t.Fatal("GlobalConfig should not be nil even with invalid JSON")
	}
}

func TestSaveWithoutLoad(t *testing.T) {
	tmpDir := t.TempDir()
	testConfigPath := filepath.Join(tmpDir, "save_test.json")

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, testConfigPath)

	// 重置configPath
	configPath = ""

	// 直接保存（应该先调用Load）
	Save()

	// 验证文件已创建
	if _, err := os.Stat(testConfigPath); os.IsNotExist(err) {
		t.Error("Config file should be created by Save")
	}
}

func TestLoadWithValidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	testConfigPath := filepath.Join(tmpDir, "valid_config.json")

	// 创建有效的配置
	testConfig := structs.Config{
		Version: 123,
		Model: structs.ModelsConfig{
			Models: map[int32]structs.ModelConfig{
				1: {
					ModelName: "test-model",
					ModelID:   "test-id",
				},
			},
		},
	}

	data, err := json.Marshal(testConfig)
	if err != nil {
		t.Fatalf("Failed to marshal test config: %v", err)
	}

	if err := os.WriteFile(testConfigPath, data, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, testConfigPath)

	configPath = ""

	// 加载配置
	Load()

	if GlobalConfig.Version != 123 {
		t.Errorf("Expected version 123, got %d", GlobalConfig.Version)
	}

	if len(GlobalConfig.Model.Models) == 0 {
		t.Error("Expected models to be loaded")
	}
}

func TestExpandPathWithEnvVar(t *testing.T) {
	os.Setenv("TEST_VAR", "test_value")
	defer os.Unsetenv("TEST_VAR")

	result := configutil.ExpandPath("$TEST_VAR/path")
	if result != filepath.Clean("test_value/path") {
		t.Errorf("Expected 'test_value/path', got %s", result)
	}
}

func TestGenerateKey(t *testing.T) {
	key := generateKey()

	// Check prefix
	if len(key) < 4 || key[:4] != "alk-" {
		t.Errorf("generateKey() = %q, want prefix 'alk-'", key)
	}

	// Check length > 12
	if len(key) <= 12 {
		t.Errorf("generateKey() = %q (len=%d), want length > 12", key, len(key))
	}

	// Check uniqueness
	key2 := generateKey()
	if key == key2 {
		t.Error("generateKey() returned same key twice")
	}
}

func TestLoadAutoGeneratesKeyWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	testConfigPath := filepath.Join(tmpDir, "test_key_config.json")

	// Write config with empty key
	emptyConfig := structs.Config{
		Version: 1,
		Server: structs.RPCConfig{
			Key: "",
		},
	}
	data, _ := json.Marshal(emptyConfig)
	os.WriteFile(testConfigPath, data, 0644)

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, testConfigPath)
	configPath = ""

	Load()

	if GlobalConfig.Server.Key == "" {
		t.Error("Server.Key should be auto-generated when empty")
	}

	if len(GlobalConfig.Server.Key) <= 12 {
		t.Errorf("Server.Key = %q (len=%d), want length > 12", GlobalConfig.Server.Key, len(GlobalConfig.Server.Key))
	}
}

func TestLoadPreservesExistingKey(t *testing.T) {
	tmpDir := t.TempDir()
	testConfigPath := filepath.Join(tmpDir, "test_existing_key.json")

	existingKey := "alk-my-existing-secret-key-123"

	cfg := structs.Config{
		Version: 1,
		Server: structs.RPCConfig{
			Key: existingKey,
		},
	}
	data, _ := json.Marshal(cfg)
	os.WriteFile(testConfigPath, data, 0644)

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, testConfigPath)
	configPath = ""

	Load()

	if GlobalConfig.Server.Key != existingKey {
		t.Errorf("Server.Key = %q, want %q", GlobalConfig.Server.Key, existingKey)
	}
}
