package loop

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/provider/request/agents"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// goal-check 调用 deactivate_agent 后，打回标记必须在工具执行完立即被消费，
// 否则 loop 会带着校验代理再请求一轮（e2e 曾表现为 goal-check 多跑一次）。
func TestConsumeGoalCheckRejection(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	session := createTestChat(db, t)
	session.CurrentAgentID = agents.GoalCheckInstanceID
	session.NowAgent = agents.GoalCheckInstanceID
	session.TemporyDataOfSession[storageStructs.TempKeyGoalCheckRejected] = "tests still failing"

	if !consumeGoalCheckRejection(session) {
		t.Fatal("expected rejection to be consumed")
	}
	if session.CurrentAgentID != "" || session.NowAgent != "" {
		t.Fatalf("goal-check runtime state not cleared: agent=%q now=%q",
			session.CurrentAgentID, session.NowAgent)
	}
	if _, ok := session.TemporyDataOfSession[storageStructs.TempKeyGoalCheckRejected]; ok {
		t.Fatal("rejection marker must be removed after consumption")
	}
	if !strings.Contains(session.SystemPrompt, "[goal-check rejected]") ||
		!strings.Contains(session.SystemPrompt, "tests still failing") {
		t.Fatalf("rejection reason not injected into main agent: %q", session.SystemPrompt)
	}
}

func TestConsumeGoalCheckRejection_NoMarker(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	session := createTestChat(db, t)
	session.CurrentAgentID = agents.GoalCheckInstanceID

	if consumeGoalCheckRejection(session) {
		t.Fatal("no marker: must not report a rejection")
	}
	if session.CurrentAgentID != agents.GoalCheckInstanceID {
		t.Fatal("goal-check agent must stay active while there is no rejection")
	}
	if session.SystemPrompt != "" {
		t.Fatalf("unexpected system prompt: %q", session.SystemPrompt)
	}
}

// 普通子代理即便残留了同名的临时数据，也不能被当成 goal-check 打回处理。
func TestConsumeGoalCheckRejection_NormalAgent(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	session := createTestChat(db, t)
	session.CurrentAgentID = "coder"
	session.TemporyDataOfSession[storageStructs.TempKeyGoalCheckRejected] = "ignored"

	if consumeGoalCheckRejection(session) {
		t.Fatal("normal agent must not consume the goal-check rejection")
	}
	if _, ok := session.TemporyDataOfSession[storageStructs.TempKeyGoalCheckRejected]; !ok {
		t.Fatal("marker must be left untouched for non goal-check agents")
	}
	if session.CurrentAgentID != "coder" {
		t.Fatal("normal agent state must not be cleared")
	}
}

func TestConsumeGoalCheckRejection_NilSession(t *testing.T) {
	if consumeGoalCheckRejection(nil) {
		t.Fatal("nil session must not report a rejection")
	}
}
