package structs

import (
	"testing"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
)

// TestEffectiveAgentConfigPriority 验证生效 agent 配置的优先级：
// 活跃子代理 > 会话 role > 零值（默认主 agent）。
func TestEffectiveAgentConfigPriority(t *testing.T) {
	roleCfg := cfgStruct.AgentConfig{AgentName: "RoleAgent", AgentPrompt: "role prompt", AgentModel: 7}
	subCfg := cfgStruct.AgentConfig{AgentName: "SubAgent", AgentPrompt: "sub prompt", AgentModel: 9}

	t.Run("无角色无子代理", func(t *testing.T) {
		c := &Chats{}
		if c.HasActiveAgent() {
			t.Fatal("HasActiveAgent() = true, want false")
		}
		if got := c.EffectiveAgentConfig(); got.AgentName != "" {
			t.Errorf("EffectiveAgentConfig().AgentName = %q, want empty", got.AgentName)
		}
	})

	t.Run("仅会话角色", func(t *testing.T) {
		c := &Chats{Role: "reviewer"}
		c.SetRoleRuntime("reviewer", roleCfg)
		if c.HasActiveAgent() {
			t.Fatal("HasActiveAgent() = true, want false")
		}
		if got := c.EffectiveAgentConfig(); got.AgentName != "RoleAgent" {
			t.Errorf("EffectiveAgentConfig().AgentName = %q, want RoleAgent", got.AgentName)
		}
	})

	t.Run("活跃子代理优先于角色", func(t *testing.T) {
		c := &Chats{
			Role:               "reviewer",
			NowAgent:           "explorer",
			CurrentAgentID:     "explorer",
			CurrentAgentConfig: subCfg,
		}
		c.SetRoleRuntime("reviewer", roleCfg)
		if !c.HasActiveAgent() {
			t.Fatal("HasActiveAgent() = false, want true")
		}
		if got := c.EffectiveAgentConfig(); got.AgentName != "SubAgent" {
			t.Errorf("EffectiveAgentConfig().AgentName = %q, want SubAgent", got.AgentName)
		}
	})
}

// TestEffectiveModelIDRole 验证模型解析：活跃子代理 > role > LastModelID。
func TestEffectiveModelIDRole(t *testing.T) {
	tests := []struct {
		name string
		chat *Chats
		want uint32
	}{
		{
			name: "无角色无子代理回退 LastModelID",
			chat: &Chats{LastModelID: 3},
			want: 3,
		},
		{
			name: "role 模型覆盖 LastModelID",
			chat: &Chats{LastModelID: 3, Role: "r", RoleConfig: cfgStruct.AgentConfig{AgentModel: 5}},
			want: 5,
		},
		{
			name: "role 模型为 0 时回退 LastModelID",
			chat: &Chats{LastModelID: 3, Role: "r", RoleConfig: cfgStruct.AgentConfig{AgentModel: 0}},
			want: 3,
		},
		{
			name: "role 模型为 -1 时回退 LastModelID",
			chat: &Chats{LastModelID: 3, Role: "r", RoleConfig: cfgStruct.AgentConfig{AgentModel: -1}},
			want: 3,
		},
		{
			name: "活跃子代理模型优先于 role",
			chat: &Chats{
				LastModelID:        3,
				Role:               "r",
				RoleConfig:         cfgStruct.AgentConfig{AgentModel: 5},
				CurrentAgentID:     "sub",
				NowAgent:           "sub",
				CurrentAgentConfig: cfgStruct.AgentConfig{AgentModel: 9},
			},
			want: 9,
		},
		{
			name: "活跃子代理无模型时回退 LastModelID 而非 role",
			chat: &Chats{
				LastModelID:        3,
				Role:               "r",
				RoleConfig:         cfgStruct.AgentConfig{AgentModel: 5},
				CurrentAgentID:     "sub",
				NowAgent:           "sub",
				CurrentAgentConfig: cfgStruct.AgentConfig{AgentModel: 0},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chat := tt.chat
			if got := chat.EffectiveModelID(); got != tt.want {
				t.Errorf("EffectiveModelID() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestSetClearRoleRuntime 验证运行时角色字段的设置/清除（不涉及持久化）。
func TestSetClearRoleRuntime(t *testing.T) {
	c := &Chats{}
	cfg := cfgStruct.AgentConfig{AgentName: "Reviewer", AgentPrompt: "review"}
	c.SetRoleRuntime("reviewer", cfg)
	if c.Role != "reviewer" || c.RoleConfig.AgentName != "Reviewer" {
		t.Fatalf("SetRoleRuntime: Role=%q RoleConfig.AgentName=%q", c.Role, c.RoleConfig.AgentName)
	}

	c.ClearRoleRuntime()
	if c.Role != "" || c.RoleConfig.AgentName != "" {
		t.Fatalf("ClearRoleRuntime: Role=%q RoleConfig.AgentName=%q", c.Role, c.RoleConfig.AgentName)
	}
}

// TestNilChatsRoleAccessors 验证 nil 接受者不会 panic。
func TestNilChatsRoleAccessors(t *testing.T) {
	var c *Chats
	if c.HasActiveAgent() {
		t.Fatal("nil HasActiveAgent() = true, want false")
	}
	if got := c.EffectiveAgentConfig(); got.AgentName != "" {
		t.Errorf("nil EffectiveAgentConfig() = %+v", got)
	}
	if got := c.EffectiveModelID(); got != 0 {
		t.Errorf("nil EffectiveModelID() = %d, want 0", got)
	}
	c.SetRoleRuntime("x", cfgStruct.AgentConfig{})
	c.ClearRoleRuntime()
}
