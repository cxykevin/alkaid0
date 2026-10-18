package request

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUserExpr(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer u.Unwrap(db.DB()).Close()

	userApprove := "(ToolCall.Name == 'edit')"
	userReject := "(ToolCall.Name == 'agent') || (ToolCall.Name == 'activate_agent') || (ToolCall.Name == 'run') || (ToolCall.Name == 'run' && hasParam(ToolCall, 'sandbox') && param(ToolCall, 'sandbox') == false)"

	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "test-agent",
		NowAgent:       "test-agent",
		CurrentAgentConfig: cfgStruct.AgentConfig{
			AutoApprove: userApprove,
			AutoReject:  userReject,
		},
	}

	// Test 1: edit tool → should be approved (AutoApprove matches)
	t.Run("edit_approved", func(t *testing.T) {
		result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "edit", ID: "1"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionApproved {
			t.Errorf("edit should be approved, got %v", result.Decision)
		}
	})

	// Test 2: agent tool → should be rejected (AutoReject matches)
	t.Run("agent_rejected", func(t *testing.T) {
		result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "agent", ID: "2"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionRejected {
			t.Errorf("agent should be rejected, got %v", result.Decision)
		}
	})

	// Test 3: run tool → should be rejected
	t.Run("run_rejected", func(t *testing.T) {
		result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "run", ID: "3"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionRejected {
			t.Errorf("run should be rejected, got %v", result.Decision)
		}
	})

	// Test 4: run with sandbox=false → should be rejected
	t.Run("run_nosandbox_rejected", func(t *testing.T) {
		falseVal := any(false)
		result, err := EvaluateApprovalRules(session, []ToolCall{{
			Name: "run", ID: "4",
			Parameters: map[string]*any{"sandbox": &falseVal},
		}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionRejected {
			t.Errorf("run sandbox=false should be rejected, got %v", result.Decision)
		}
	})

	// Test 5: scope tool → neither approve nor reject → DecisionManual (unless builtin rules)
	t.Run("scope_manual_without_builtin", func(t *testing.T) {
		// Temporarily enable builtin rules
		oldIgnore := config.GlobalConfig.Agent.IgnoreDefaultRules
		config.GlobalConfig.Agent.IgnoreDefaultRules = true
		defer func() { config.GlobalConfig.Agent.IgnoreDefaultRules = oldIgnore }()

		result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "scope", ID: "5"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionManual {
			t.Errorf("scope should be manual (no rules match), got %v", result.Decision)
		}
	})

	t.Log("All user expression tests passed!")
}

// TestEvaluateApprovalRulesWorkdir 验证规则表达式中的 Workdir 变量：
// 值为会话根目录 + 当前子代理绑定路径，并且审批/拒绝规则都能读到。
func TestEvaluateApprovalRulesWorkdir(t *testing.T) {
	oldIgnore := config.GlobalConfig.Agent.IgnoreDefaultRules
	config.GlobalConfig.Agent.IgnoreDefaultRules = true
	defer func() { config.GlobalConfig.Agent.IgnoreDefaultRules = oldIgnore }()

	root := filepath.Join(string(filepath.Separator), "workspace", "proj")
	bind := filepath.Join("packages", "api")
	session := &storageStructs.Chats{
		Root:                root,
		CurrentActivatePath: bind,
		CurrentAgentID:      "test-agent",
		NowAgent:            "test-agent",
	}

	wantWorkdir := filepath.Join(root, bind)
	if got := approvalWorkdir(session); got != wantWorkdir {
		t.Fatalf("approvalWorkdir = %q, want %q", got, wantWorkdir)
	}

	t.Run("approve_rule_sees_workdir", func(t *testing.T) {
		session.CurrentAgentConfig = cfgStruct.AgentConfig{
			AutoApprove: "regex('proj', Workdir) && regex('packages', Workdir)",
			AutoReject:  "false",
		}
		result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "run", ID: "1"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionApproved {
			t.Errorf("run should be approved by Workdir rule, got %v", result.Decision)
		}
	})

	t.Run("reject_rule_sees_workdir", func(t *testing.T) {
		session.CurrentAgentConfig = cfgStruct.AgentConfig{
			AutoApprove: "true",
			AutoReject:  "regex('workspace', Workdir)",
		}
		result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "run", ID: "2"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionRejected {
			t.Errorf("run should be rejected by Workdir rule, got %v", result.Decision)
		}
	})

	t.Run("workdir_ignores_empty_bind_path", func(t *testing.T) {
		session.CurrentAgentConfig = cfgStruct.AgentConfig{
			AutoApprove: "Workdir == '" + strings.ReplaceAll(root, `\`, `\\`) + "'",
			AutoReject:  "false",
		}
		session.CurrentActivatePath = ""
		defer func() { session.CurrentActivatePath = bind }()
		result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "run", ID: "3"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionApproved {
			t.Errorf("run should be approved when bind path is empty, got %v", result.Decision)
		}
	})

	t.Run("empty_workdir_for_empty_root", func(t *testing.T) {
		session.CurrentAgentConfig = cfgStruct.AgentConfig{
			AutoApprove: "Workdir == ''",
			AutoReject:  "false",
		}
		session.Root = ""
		session.CurrentActivatePath = ""
		defer func() {
			session.Root = root
			session.CurrentActivatePath = bind
		}()
		result, err := EvaluateApprovalRules(session, []ToolCall{{Name: "run", ID: "4"}})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Decision != DecisionApproved {
			t.Errorf("empty root should yield empty Workdir, got %v", result.Decision)
		}
	})
}
