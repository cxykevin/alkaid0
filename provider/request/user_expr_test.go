package request

import (
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
