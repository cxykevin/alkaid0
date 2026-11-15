package run

import (
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// TestSandboxDisabledByConfigUsesEffectiveAgent 验证沙盒禁用开关读取的是
// EffectiveAgentConfig（活跃子代理 > 会话 role > 默认主 agent），
// 并与全局配置 / 环境变量取或。
func TestSandboxDisabledByConfigUsesEffectiveAgent(t *testing.T) {
	restore := config.GlobalConfigSwap(cfgStructs.Config{})
	defer restore()
	t.Setenv("ALKAID0_DISABLE_SANDBOX", "false")

	session := &structs.Chats{}
	if sandboxDisabledByConfig(session) {
		t.Fatal("默认应启用沙盒")
	}
	if sandboxDisabledByConfig(nil) {
		t.Fatal("nil session 应启用沙盒")
	}

	// role 显式保持沙盒
	session.SetRoleRuntime("sandboxed-role", cfgStructs.AgentConfig{DisableSandbox: false})
	if sandboxDisabledByConfig(session) {
		t.Error("role 未配置 DisableSandbox 时应启用沙盒")
	}

	// role 关闭沙盒
	session.SetRoleRuntime("open-role", cfgStructs.AgentConfig{DisableSandbox: true})
	if !sandboxDisabledByConfig(session) {
		t.Error("role 的 DisableSandbox 应生效")
	}

	// 活跃子代理优先于 role
	session.NowAgent = "sub"
	session.CurrentAgentConfig = cfgStructs.AgentConfig{DisableSandbox: false}
	if sandboxDisabledByConfig(session) {
		t.Error("活跃子代理显式启用沙盒时应忽略 role 的 DisableSandbox")
	}

	// 全局配置
	session.NowAgent = ""
	session.CurrentAgentConfig = cfgStructs.AgentConfig{}
	session.ClearRoleRuntime()
	config.GlobalConfig.Agent.DisableSandbox = true
	if !sandboxDisabledByConfig(session) {
		t.Error("全局 DisableSandbox 应生效")
	}

	// 环境变量
	config.GlobalConfig.Agent.DisableSandbox = false
	t.Setenv("ALKAID0_DISABLE_SANDBOX", "true")
	if !sandboxDisabledByConfig(session) {
		t.Error("ALKAID0_DISABLE_SANDBOX=true 应生效")
	}
}
