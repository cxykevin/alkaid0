package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadHooksConfig 验证 Hooks 随配置文件加载并原样保留
// （默认值填充不会清空/覆盖用户配置的钩子命令）。
func TestLoadHooksConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
    "Version": 1,
    "Hooks": {
        "OnUserMessage": ["echo user"],
        "OnTaskFinish": ["echo finish", "echo again"]
    }
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}

	oldEnv := os.Getenv(envConfigName)
	t.Cleanup(func() { _ = os.Setenv(envConfigName, oldEnv) })
	_ = os.Setenv(envConfigName, path)
	configPath = ""
	t.Cleanup(func() { configPath = "" })

	Load()

	hooks := GlobalConfig.Hooks
	if len(hooks.OnUserMessage) != 1 || hooks.OnUserMessage[0] != "echo user" {
		t.Fatalf("OnUserMessage = %#v, want [\"echo user\"]", hooks.OnUserMessage)
	}
	if len(hooks.OnTaskFinish) != 2 || hooks.OnTaskFinish[1] != "echo again" {
		t.Fatalf("OnTaskFinish = %#v, want [\"echo finish\" \"echo again\"]", hooks.OnTaskFinish)
	}
	if len(hooks.OnCronjobWakeup) != 0 {
		t.Fatalf("OnCronjobWakeup = %#v, want empty", hooks.OnCronjobWakeup)
	}
}
