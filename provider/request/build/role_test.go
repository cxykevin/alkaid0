package build

import (
	"strings"
	"testing"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// TestBuildRolePromptReplacesDefault 验证会话 role 的 AgentPrompt 替换默认主 agent 提示词，
// 并且 role 的审批规则说明也会进入 system（Build 使用 EffectiveAgentConfig）。
func TestBuildRolePromptReplacesDefault(t *testing.T) {
	db := setupBuildTest(t)

	chat := structs.Chats{
		ID:          1,
		LastModelID: 1,
		DB:          db,
		InTestFlag:  true,
		Role:        "reviewer",
		RoleConfig: cfgStruct.AgentConfig{
			AgentPrompt: "ROLE-PROMPT-MARKER: review only",
			AutoApprove: `ToolCall.Name == "read"`,
			AutoReject:  `ToolCall.Name == "edit"`,
		},
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	result, err := Build(db, &chat)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(result.Messages) == 0 || result.Messages[0].Role != "system" {
		t.Fatalf("Build() 首条消息不是 system: %+v", result.Messages)
	}
	system := result.Messages[0].Content

	if !strings.Contains(system, "ROLE-PROMPT-MARKER") {
		t.Error("role 的 AgentPrompt 未进入 system")
	}
	if strings.Contains(system, "Alkaid Default Agent") {
		t.Error("role 生效时不应再拼接默认主 agent 提示词")
	}
	if !strings.Contains(system, `Auto-Approve: ToolCall.Name == "read"`) {
		t.Error("role 的 AutoApprove 未进入 system 的审批说明")
	}
	if !strings.Contains(system, `Auto-Reject: ToolCall.Name == "edit"`) {
		t.Error("role 的 AutoReject 未进入 system 的审批说明")
	}
}

// TestBuildNoRoleKeepsDefaultPrompt 验证无 role 时行为不变（仍使用默认主 agent 提示词）。
func TestBuildNoRoleKeepsDefaultPrompt(t *testing.T) {
	db := setupBuildTest(t)

	chat := structs.Chats{
		ID:          2,
		LastModelID: 1,
		DB:          db,
		InTestFlag:  true,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	result, err := Build(db, &chat)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("Build() 没有消息")
	}
	if !strings.Contains(result.Messages[0].Content, "Alkaid Default Agent") {
		t.Error("无 role 时 system 应包含默认主 agent 提示词")
	}
}
