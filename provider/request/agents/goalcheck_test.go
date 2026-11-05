package agents

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

func TestActivateInternalAgent(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	// GoalCheckModel > 0 时覆盖内置 -1。
	config.GlobalConfig.Agent.GoalCheckModel = 2

	if err := ActivateInternalAgent(session, GoalCheckAgentID, GoalCheckInstanceID, "please verify"); err != nil {
		t.Fatalf("ActivateInternalAgent failed: %v", err)
	}
	if session.CurrentAgentID != GoalCheckInstanceID {
		t.Fatalf("CurrentAgentID = %q", session.CurrentAgentID)
	}
	if !session.CurrentAgentConfig.Internal {
		t.Fatal("goal-check config must be internal")
	}
	if session.CurrentAgentConfig.AgentModel != 2 {
		t.Fatalf("GoalCheckModel override not applied: %d", session.CurrentAgentConfig.AgentModel)
	}
	// prompt 作为 goal-check 自己的消息落库，主 agent 历史不会看到它。
	var msgs []structs.Messages
	if err := db.Where("chat_id = ? AND agent_id = ?", session.ID, GoalCheckInstanceID).Find(&msgs).Error; err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Delta != "please verify" {
		t.Fatalf("unexpected goal-check messages: %#v", msgs)
	}
	// 不写 chats.now_agent（内部代理不跨进程恢复）。
	var stored structs.Chats
	if err := db.Select("now_agent").Where("id = ?", session.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.NowAgent != "" {
		t.Fatalf("internal agent must not persist now_agent: %q", stored.NowAgent)
	}

	ClearInternalAgent(session)
	if session.CurrentAgentID != "" || session.NowAgent != "" || session.CurrentAgentConfig.Internal {
		t.Fatal("ClearInternalAgent did not reset runtime state")
	}
}

func TestActivateInternalAgent_RejectsNormalAgent(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	if err := ActivateInternalAgent(session, "tag-coder", "coder", "x"); err == nil {
		t.Fatal("normal user agent must not be activated through the internal path")
	}
}

func TestLastAgentOutputAndAbortFallback(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 子代理最后输出
	agentID := "worker"
	if err := db.Create(&structs.Messages{
		ChatID:  session.ID,
		Type:    structs.MessagesRoleAgent,
		Delta:   "worker final answer",
		AgentID: &agentID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if got := LastAgentOutput(session, agentID); got != "worker final answer" {
		t.Fatalf("LastAgentOutput = %q", got)
	}

	session.CurrentAgentID = agentID
	session.NowAgent = agentID
	if err := AbortAgentWithOutput(session); err != nil {
		t.Fatalf("AbortAgentWithOutput failed: %v", err)
	}
	if session.CurrentAgentID != "" || session.NowAgent != "" {
		t.Fatal("fallback must clear the active subagent")
	}
	var mainMsgs []structs.Messages
	if err := db.Where("chat_id = ? AND (agent_id = '' OR agent_id IS NULL)", session.ID).
		Find(&mainMsgs).Error; err != nil {
		t.Fatal(err)
	}
	if len(mainMsgs) != 1 {
		t.Fatalf("expected one fallback message, got %d", len(mainMsgs))
	}
	if !strings.Contains(mainMsgs[0].Delta, "without deactivate") ||
		!strings.Contains(mainMsgs[0].Delta, "worker final answer") {
		t.Fatalf("unexpected fallback content: %q", mainMsgs[0].Delta)
	}
}

func TestAbortAgentWithoutOutput(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	session.CurrentAgentID = "silent-worker"
	session.NowAgent = "silent-worker"

	if err := AbortAgentWithOutput(session); err != nil {
		t.Fatalf("AbortAgentWithOutput failed: %v", err)
	}
	var mainMsgs []structs.Messages
	if err := db.Where("chat_id = ? AND (agent_id = '' OR agent_id IS NULL)", session.ID).
		Find(&mainMsgs).Error; err != nil {
		t.Fatal(err)
	}
	if len(mainMsgs) != 1 || !strings.Contains(mainMsgs[0].Delta, "ended abnormally without any output") {
		t.Fatalf("expected explicit error fallback, got %#v", mainMsgs)
	}
}

func TestAbortAgentWithOutput_NoActiveAgent(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	if err := AbortAgentWithOutput(session); err != nil {
		t.Fatalf("idle session should be a no-op: %v", err)
	}
}
