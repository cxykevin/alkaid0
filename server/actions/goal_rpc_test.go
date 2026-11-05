package actions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// goalTestConn 测试连接 id（与其它用例的 id 段错开）。
const goalTestConn = uint64(9302)

// setupGoalRPC 准备带 .alkaid0 的临时工作目录、一个空会话以及连接绑定。
func setupGoalRPC(t *testing.T) (dir string, sessionID string, chatID uint32) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("create .alkaid0: %v", err)
	}
	db, err := loadDB(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	chat := structs.Chats{}
	if err := db.Create(&chat).Error; err != nil {
		closeDB(dir)
		t.Fatalf("create chat: %v", err)
	}
	chatID = chat.ID
	closeDB(dir)

	sessionID = cwd2SessionID(dir, chatID)
	bindedSessionOnConnMu.Lock()
	if bindedSessionOnConn == nil {
		bindedSessionOnConn = map[uint64][]string{}
	}
	bindedSessionOnConn[goalTestConn] = []string{sessionID}
	bindedSessionOnConnMu.Unlock()
	t.Cleanup(func() {
		bindedSessionOnConnMu.Lock()
		delete(bindedSessionOnConn, goalTestConn)
		bindedSessionOnConnMu.Unlock()
	})
	return dir, sessionID, chatID
}

func readGoalFromDB(t *testing.T, dir string, chatID uint32) string {
	t.Helper()
	db, err := loadDB(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer closeDB(dir)
	var chat structs.Chats
	if err := db.Select("goal").Where("id = ?", chatID).Limit(1).Find(&chat).Error; err != nil {
		t.Fatalf("query goal: %v", err)
	}
	return chat.Goal
}

func TestGoalRPCSetGetCold(t *testing.T) {
	dir, sessionID, chatID := setupGoalRPC(t)

	resp, err := GoalSet(GoalSetRequest{SessionID: sessionID, Goal: "  finish the tests  "}, nil, goalTestConn)
	if err != nil {
		t.Fatalf("GoalSet: %v", err)
	}
	if resp.Goal != "finish the tests" {
		t.Fatalf("goal not trimmed: %q", resp.Goal)
	}
	if resp.UpdatedAt == "" {
		t.Fatal("expected updatedAt in response")
	}
	if got := readGoalFromDB(t, dir, chatID); got != "finish the tests" {
		t.Fatalf("goal not persisted: %q", got)
	}

	got, err := GoalGet(GoalGetRequest{SessionID: sessionID}, nil, goalTestConn)
	if err != nil {
		t.Fatalf("GoalGet: %v", err)
	}
	if got.Goal != "finish the tests" || got.UpdatedAt == "" {
		t.Fatalf("unexpected get response: %#v", got)
	}

	// 空字符串 = 清除。
	if _, err := GoalSet(GoalSetRequest{SessionID: sessionID, Goal: ""}, nil, goalTestConn); err != nil {
		t.Fatalf("GoalSet clear: %v", err)
	}
	if got := readGoalFromDB(t, dir, chatID); got != "" {
		t.Fatalf("goal not cleared: %q", got)
	}
	cleared, err := GoalGet(GoalGetRequest{SessionID: sessionID}, nil, goalTestConn)
	if err != nil {
		t.Fatalf("GoalGet after clear: %v", err)
	}
	if cleared.Goal != "" {
		t.Fatalf("expected empty goal, got %q", cleared.Goal)
	}
}

func TestGoalRPCMaxLength(t *testing.T) {
	_, sessionID, _ := setupGoalRPC(t)

	maxGoal := strings.Repeat("字", structs.MaxGoalRunes)
	if _, err := GoalSet(GoalSetRequest{SessionID: sessionID, Goal: maxGoal}, nil, goalTestConn); err != nil {
		t.Fatalf("goal at max length should be accepted: %v", err)
	}
	if _, err := GoalSet(GoalSetRequest{SessionID: sessionID, Goal: maxGoal + "x"}, nil, goalTestConn); err == nil ||
		!strings.Contains(err.Error(), "too long") {
		t.Fatalf("expected too-long error, got %v", err)
	}
}

func TestGoalRPCRequiresBinding(t *testing.T) {
	_, sessionID, _ := setupGoalRPC(t)

	if _, err := GoalGet(GoalGetRequest{SessionID: sessionID}, nil, goalTestConn+1); err == nil ||
		!strings.Contains(err.Error(), "not bound") {
		t.Fatalf("expected binding error, got %v", err)
	}
	if _, err := GoalSet(GoalSetRequest{SessionID: sessionID, Goal: "x"}, nil, goalTestConn+1); err == nil ||
		!strings.Contains(err.Error(), "not bound") {
		t.Fatalf("expected binding error, got %v", err)
	}
}

func TestGoalCommandSetShowClear(t *testing.T) {
	dir, _, chatID := setupGoalRPC(t)
	db, err := loadDB(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer closeDB(dir)
	sess := &structs.Chats{ID: chatID, DB: db}
	obj := &sessionObj{cwd: dir, id: chatID, session: sess}

	if err := goalCommand(obj, ""); err != nil {
		t.Fatalf("goalCommand show: %v", err)
	}
	if err := goalCommand(obj, "  ship the feature  "); err != nil {
		t.Fatalf("goalCommand set: %v", err)
	}
	if sess.Goal != "ship the feature" {
		t.Fatalf("goal not trimmed: %q", sess.Goal)
	}
	if got := readGoalFromDB(t, dir, chatID); got != "ship the feature" {
		t.Fatalf("goal not persisted: %q", got)
	}

	// clear 不区分大小写。
	if err := goalCommand(obj, "CLEAR"); err != nil {
		t.Fatalf("goalCommand clear: %v", err)
	}
	if sess.Goal != "" {
		t.Fatalf("goal not cleared in memory: %q", sess.Goal)
	}
	if got := readGoalFromDB(t, dir, chatID); got != "" {
		t.Fatalf("goal not cleared in db: %q", got)
	}
}

func TestGoalRPCUnknownSession(t *testing.T) {
	dir, _, chatID := setupGoalRPC(t)

	missingID := cwd2SessionID(dir, chatID+100)
	bindedSessionOnConnMu.Lock()
	bindedSessionOnConn[goalTestConn] = append(bindedSessionOnConn[goalTestConn], missingID)
	bindedSessionOnConnMu.Unlock()

	if _, err := GoalGet(GoalGetRequest{SessionID: missingID}, nil, goalTestConn); err == nil ||
		!strings.Contains(err.Error(), "session not found") {
		t.Fatalf("expected session-not-found, got %v", err)
	}
	if _, err := GoalSet(GoalSetRequest{SessionID: missingID, Goal: "x"}, nil, goalTestConn); err == nil ||
		!strings.Contains(err.Error(), "session not found") {
		t.Fatalf("expected session-not-found, got %v", err)
	}
}
