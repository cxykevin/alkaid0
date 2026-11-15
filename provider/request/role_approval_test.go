package request

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestEvaluateApprovalRules_RoleConfig 验证主会话 role 的 AutoApprove/AutoReject
// 会作为 Agent 级规则参与评估，且表达式环境中的 Agent 是 role 配置。
func TestEvaluateApprovalRules_RoleConfig(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	restore := config.GlobalConfigSwap(cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{IgnoreDefaultRules: true},
	})
	defer restore()

	session := &storageStructs.Chats{
		ID:   1,
		DB:   db,
		Role: "reviewer",
		RoleConfig: cfgStruct.AgentConfig{
			AgentModel:  4,
			AutoApprove: `ToolCall.Name == "read" && Agent.AgentModel == 4`,
			AutoReject:  `ToolCall.Name == "edit"`,
		},
	}

	// role 的拒绝规则生效
	result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "edit", ID: "1"}})
	if err != nil {
		t.Fatalf("EvaluateApprovalRules(edit) error = %v", err)
	}
	if result.Decision != DecisionRejected {
		t.Errorf("role AutoReject: decision = %v, want rejected", result.Decision)
	}

	// role 的审批规则生效，且 Agent 变量来自 role 配置
	result, err = EvaluateApprovalRules(session, []ToolCall{{Name: "read", ID: "2"}})
	if err != nil {
		t.Fatalf("EvaluateApprovalRules(read) error = %v", err)
	}
	if result.Decision != DecisionApproved {
		t.Errorf("role AutoApprove: decision = %v, want approved", result.Decision)
	}

	// 无规则命中 → 手动审批
	result, err = EvaluateApprovalRules(session, []ToolCall{{Name: "run", ID: "3"}})
	if err != nil {
		t.Fatalf("EvaluateApprovalRules(run) error = %v", err)
	}
	if result.Decision != DecisionManual {
		t.Errorf("unknown tool: decision = %v, want manual", result.Decision)
	}
}

// TestEvaluateApprovalRules_RoleEmptyFallsBackToMainAgent 验证 role 的审批/拒绝为空时
// 语义与主 agent 完全一致：回退全局默认（此处关闭内置规则以便断言）。
func TestEvaluateApprovalRules_RoleEmptyFallsBackToMainAgent(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	restore := config.GlobalConfigSwap(cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			IgnoreDefaultRules: true,
			DefaultAutoApprove: `ToolCall.Name == "read"`,
			DefaultAutoReject:  `ToolCall.Name == "run"`,
		},
	})
	defer restore()

	session := &storageStructs.Chats{
		ID:   1,
		DB:   db,
		Role: "silent-role",
		RoleConfig: cfgStruct.AgentConfig{
			AgentPrompt: "role without approval rules",
			// AutoApprove / AutoReject 均为空
		},
	}

	result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "read", ID: "1"}})
	if err != nil {
		t.Fatalf("EvaluateApprovalRules(read) error = %v", err)
	}
	if result.Decision != DecisionApproved {
		t.Errorf("空 role 规则应回退全局 AutoApprove: decision = %v, want approved", result.Decision)
	}

	result, err = EvaluateApprovalRules(session, []ToolCall{{Name: "run", ID: "2"}})
	if err != nil {
		t.Fatalf("EvaluateApprovalRules(run) error = %v", err)
	}
	if result.Decision != DecisionRejected {
		t.Errorf("空 role 规则应回退全局 AutoReject: decision = %v, want rejected", result.Decision)
	}
}

// TestEffectiveAgentConfigIgnoresRoleDuringSubAgent 验证子代理激活期间 role 不生效，
// 且表达式环境同样使用子代理配置（停用后自动回到 role）。
func TestEffectiveAgentConfigIgnoresRoleDuringSubAgent(t *testing.T) {
	chat := &storageStructs.Chats{
		Role:               "reviewer",
		NowAgent:           "explorer",
		CurrentAgentID:     "explorer",
		CurrentAgentConfig: cfgStruct.AgentConfig{AutoApprove: `ToolCall.Name == "sub"`},
	}
	chat.SetRoleRuntime("reviewer", cfgStruct.AgentConfig{AutoReject: `ToolCall.Name == "role"`})
	if got := chat.EffectiveAgentConfig(); !strings.Contains(got.AutoApprove, "sub") {
		t.Errorf("子代理激活时 EffectiveAgentConfig = %+v, want subagent config", got)
	}
	if got := chat.EffectiveAgentConfig(); got.AutoReject != "" {
		t.Errorf("子代理激活时不应混入 role 规则, got AutoReject=%q", got.AutoReject)
	}
}
