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

// TestExpandPath 验证路径展开。
//
// 旧断言是
//
//	tt.contains != "" && result != filepath.Clean(tt.input) && !filepath.IsAbs(result) && ...^[0] != '~'
//
// 三个用例都无法让它成立（tilde 用例 IsAbs 为真、绝对路径用例 result 相等、
// 空用例 contains 为空），即这个测试对任何输入都恒为真、永远不会失败。
// 改为断言真实期望值。
func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir failed: %v", err)
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "tilde expansion", input: "~/test", want: filepath.Join(home, "test")},
		{name: "no tilde", input: "/absolute/path", want: "/absolute/path"},
		{name: "relative path", input: "relative/path", want: "relative/path"},
		// filepath.Clean("") == "."，这是 ExpandPath 的既有语义
		{name: "empty path", input: "", want: "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configutil.ExpandPath(tt.input)
			if got != filepath.Clean(tt.want) {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.input, got, filepath.Clean(tt.want))
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

// ---- 写时复制（copy-on-write）不变量回归测试 ----
//
// 背景：旧实现里 GlobalConfigForWrite 返回全局对象指针、调用方在锁内就地修改；
// Load 也用 *GlobalConfig = *tempConfig 就地改写已发布对象。而全仓库 200+ 处
// 读取是无锁的（直接 config.GlobalConfig），只要读者正在遍历这些 map，
// Go 运行时就会 fatal("concurrent map read and map write") 终止整个进程。
// 修复后：对象一旦发布就不再被修改，写入方克隆副本、提交时才整体替换指针。

func TestGlobalConfigForWriteIsCopyOnWrite(t *testing.T) {
	restore := GlobalConfigSwap(*GlobalConfigSafe())
	defer restore()

	published := GlobalConfigSafe() // 模拟"读者已经持有的指针"
	const probePort = uint16(45678)

	cfg, commit, _ := GlobalConfigForWrite()
	cfg.Server.Port = probePort
	if cfg.Model.Models == nil {
		cfg.Model.Models = map[int32]structs.ModelConfig{}
	}
	cfg.Model.Models[4242] = structs.ModelConfig{ModelName: "cow-probe"}
	commit()

	if published.Server.Port == probePort {
		t.Error("已发布对象被就地修改：写时复制不变量被破坏")
	}
	if _, ok := published.Model.Models[4242]; ok {
		t.Error("已发布对象的 map 被就地修改：写时复制不变量被破坏")
	}

	if got := GlobalConfigSafe().Server.Port; got != probePort {
		t.Errorf("commit 后新配置未生效：Server.Port=%d, want %d", got, probePort)
	}
	if _, ok := GlobalConfigSafe().Model.Models[4242]; !ok {
		t.Error("commit 后新配置缺少新增的模型")
	}
}

func TestGlobalConfigForWriteDiscardPublishesNothing(t *testing.T) {
	restore := GlobalConfigSwap(*GlobalConfigSafe())
	defer restore()

	before := GlobalConfigSafe().Server.Port
	cfg, _, discard := GlobalConfigForWrite()
	cfg.Server.Port = before + 1
	discard()

	if got := GlobalConfigSafe().Server.Port; got != before {
		t.Errorf("discard 之后不应发布任何改动：Server.Port=%d, want %d", got, before)
	}
}

// TestSnapshotJSONIsDecoupled 快照必须与全局对象解耦
func TestSnapshotJSONIsDecoupled(t *testing.T) {
	restore := GlobalConfigSwap(*GlobalConfigSafe())
	defer restore()

	snap, err := SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON failed: %v", err)
	}
	if len(snap) == 0 {
		t.Fatal("snapshot should not be empty")
	}

	// 之后的写入不得改变已生成的快照
	const probePort = uint16(45679)
	cfg, commit, _ := GlobalConfigForWrite()
	cfg.Server.Port = probePort
	commit()

	var decoded structs.Config
	if err := json.Unmarshal(snap, &decoded); err != nil {
		t.Fatalf("snapshot should be valid JSON: %v", err)
	}
	if decoded.Server.Port == probePort {
		t.Error("已生成的快照被之后的写入改变了（快照未解耦）")
	}
}

// TestLoadKeepsUnreadableConfigFileUntouched 回归：配置"读取失败"（但文件并非不存在）
// 时，不得把磁盘上的原配置改名备份或覆盖成默认值。
//
// 旧实现只判断 err != nil，于是权限错误、I/O 错误、路径其实是目录等情况都会走
// "改名备份 + Save() 默认配置"分支——磁盘上的用户配置被清空成默认值，而这类
// 读取失败往往只是暂时性的。这里用"配置路径是一个目录"构造 EISDIR 型读取错误
// （跨平台、且不依赖"非 root 用户"权限），修复后该目录必须原样保留。
func TestLoadKeepsUnreadableConfigFileUntouched(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.Mkdir(cfgPath, 0755); err != nil {
		t.Fatal(err)
	}

	oldEnv := os.Getenv(envConfigName)
	oldPath := configPath
	t.Cleanup(func() {
		os.Setenv(envConfigName, oldEnv)
		configPath = oldPath
	})
	os.Setenv(envConfigName, cfgPath)
	configPath = ""

	Load()

	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("配置路径在 Load 后消失（被改名或覆盖）: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("配置目录被覆盖成了普通文件：原配置被清空")
	}
	if _, err := os.Stat(cfgPath + ".bak"); err == nil {
		t.Error("读取失败不应触发改名备份")
	}
}
