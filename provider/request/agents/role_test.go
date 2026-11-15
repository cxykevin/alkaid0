package agents

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// setupRoleConfig 写入一个只含自定义 agent 的配置；builtin agent 仍然可用。
func setupRoleConfig(t *testing.T) {
	t.Helper()
	restore := config.GlobalConfigSwap(cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			Agents: map[string]cfgStruct.AgentConfig{
				"reviewer": {
					AgentName:   "Reviewer",
					AgentPrompt: "review the code",
					AgentModel:  3,
				},
				"internal-test": {
					AgentName: "Internal Test",
					Internal:  true,
				},
			},
		},
	})
	t.Cleanup(restore)
}

func TestResolveRole(t *testing.T) {
	setupRoleConfig(t)

	t.Run("空 tag 表示无角色", func(t *testing.T) {
		cfg, err := ResolveRole("")
		if err != nil {
			t.Fatalf("ResolveRole(\"\") error = %v", err)
		}
		if cfg.AgentName != "" {
			t.Errorf("ResolveRole(\"\") = %+v, want zero config", cfg)
		}
	})

	t.Run("用户配置优先", func(t *testing.T) {
		cfg, err := ResolveRole("reviewer")
		if err != nil {
			t.Fatalf("ResolveRole(reviewer) error = %v", err)
		}
		if cfg.AgentName != "Reviewer" || cfg.AgentModel != 3 {
			t.Errorf("ResolveRole(reviewer) = %+v", cfg)
		}
	})

	t.Run("内置 tag 可用", func(t *testing.T) {
		if _, err := ResolveRole("explore"); err != nil {
			t.Errorf("ResolveRole(explore) error = %v", err)
		}
	})

	t.Run("Internal 拒绝", func(t *testing.T) {
		_, err := ResolveRole("internal-test")
		if err == nil || !strings.Contains(err.Error(), "internal") {
			t.Errorf("ResolveRole(internal-test) error = %v, want internal error", err)
		}
		// 内置内部代理（goal-check）同样不可选。
		if _, err := ResolveRole("goal-check"); err == nil || !strings.Contains(err.Error(), "internal") {
			t.Errorf("ResolveRole(goal-check) error = %v, want internal error", err)
		}
	})

	t.Run("未知 tag 报错", func(t *testing.T) {
		_, err := ResolveRole("no-such-role")
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("ResolveRole(no-such-role) error = %v, want not found", err)
		}
	})
}

func TestRoleOptions(t *testing.T) {
	setupRoleConfig(t)

	options := RoleOptions()
	if len(options) == 0 {
		t.Fatal("RoleOptions() is empty")
	}
	found := map[string]RoleOption{}
	for _, opt := range options {
		found[opt.Name] = opt
	}
	if _, ok := found["reviewer"]; !ok {
		t.Error("RoleOptions() 缺少用户配置的 reviewer")
	}
	if got := found["reviewer"].DisplayName; got != "Reviewer" {
		t.Errorf("reviewer DisplayName = %q, want Reviewer", got)
	}
	if _, ok := found["explore"]; !ok {
		t.Error("RoleOptions() 缺少内置 explore")
	}
	if _, ok := found["internal-test"]; ok {
		t.Error("RoleOptions() 不应包含 Internal 代理 internal-test")
	}
	if _, ok := found["goal-check"]; ok {
		t.Error("RoleOptions() 不应包含 Internal 代理 goal-check")
	}
	// 排序：按 name 升序
	for i := 1; i < len(options); i++ {
		if options[i-1].Name > options[i].Name {
			t.Fatalf("RoleOptions() 未按 name 排序: %q > %q", options[i-1].Name, options[i].Name)
		}
	}
}

func TestLoadRole(t *testing.T) {
	setupRoleConfig(t)

	t.Run("正常加载", func(t *testing.T) {
		session := &structs.Chats{ID: 1, Role: "reviewer"}
		LoadRole(session)
		if session.RoleConfig.AgentName != "Reviewer" {
			t.Errorf("LoadRole() RoleConfig.AgentName = %q, want Reviewer", session.RoleConfig.AgentName)
		}
	})

	t.Run("空角色清空运行时配置", func(t *testing.T) {
		session := &structs.Chats{ID: 2, RoleConfig: cfgStruct.AgentConfig{AgentName: "stale"}}
		LoadRole(session)
		if session.RoleConfig.AgentName != "" {
			t.Errorf("LoadRole() 应清空 RoleConfig, got %q", session.RoleConfig.AgentName)
		}
	})

	t.Run("未知 tag 只告警并回退默认", func(t *testing.T) {
		session := &structs.Chats{ID: 3, Role: "no-such-role", RoleConfig: cfgStruct.AgentConfig{AgentName: "stale"}}
		LoadRole(session)
		if session.RoleConfig.AgentName != "" {
			t.Errorf("未知 tag 应回退零值配置, got %q", session.RoleConfig.AgentName)
		}
		// 持久化值保留，配置恢复后无需重新设置即可生效。
		if session.Role != "no-such-role" {
			t.Errorf("LoadRole() 不应清空 Role, got %q", session.Role)
		}
	})

	t.Run("nil session 不 panic", func(t *testing.T) {
		LoadRole(nil)
	})
}
