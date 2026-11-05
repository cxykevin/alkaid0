package build

import (
	"strings"
	"testing"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
)

func TestGoalBlock(t *testing.T) {
	if got := GoalBlock("   "); got != "" {
		t.Fatalf("empty goal should render empty block, got %q", got)
	}
	got := GoalBlock("finish the task")
	if got != "<goal>\nfinish the task\n</goal>" {
		t.Fatalf("unexpected block: %q", got)
	}
}

// TestGoalInjectedAtSystemTop goal 必须出现在 system 消息开头（messages[0] 的最顶端），
// 不参与 trace 落位/差分；空 goal 时完全不出现。
func TestGoalInjectedAtSystemTop(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	session := &structs.Chats{Goal: "ship the feature"}
	req, err := RequestBody(71, 1, "", noTools(), db, "SCOPES", "", cfgStruct.AgentConfig{}, session)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		t.Fatalf("first message must be system: %#v", req.Messages)
	}
	system := req.Messages[0].Content
	if !strings.HasPrefix(system, "<goal>\nship the feature\n</goal>") {
		t.Fatalf("goal is not at the top of system: %q", system)
	}
	if !strings.Contains(system, "SCOPES") {
		t.Fatal("system content lost its normal sections")
	}

	session.Goal = ""
	req, err = RequestBody(71, 1, "", noTools(), db, "SCOPES", "", cfgStruct.AgentConfig{}, session)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	if strings.Contains(req.Messages[0].Content, "<goal>") {
		t.Fatalf("empty goal must not be injected: %q", req.Messages[0].Content)
	}
}

// TestMissionControlSummaryGoal 摘要/标题请求把 goal 追加在 system 末尾（不是最顶端）。
func TestMissionControlSummaryGoal(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)
	chat := &structs.Chats{Goal: "keep going"}
	if err := db.Create(chat).Error; err != nil {
		t.Fatal(err)
	}
	// 摘要需要至少一条对话消息
	if err := db.Create(&structs.Messages{
		ChatID: chat.ID,
		Type:   structs.MessagesRoleUser,
		Delta:  "hello",
	}).Error; err != nil {
		t.Fatal(err)
	}
	_, req, err := Summary(chat.ID, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}
	if req == nil || len(req.Messages) == 0 {
		t.Fatal("expected summary request")
	}
	system := req.Messages[0].Content
	if !strings.HasSuffix(system, "<goal>\nkeep going\n</goal>") {
		t.Fatalf("summary goal should be appended at system end: %q", system)
	}
}
