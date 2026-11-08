package goal

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
)

//go:fix inline
func anyPtr(v any) *any { return new(v) }

func newTestSession(t *testing.T) *structs.Chats {
	t.Helper()
	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	chat := &structs.Chats{}
	if err := db.Create(chat).Error; err != nil {
		t.Fatal(err)
	}
	return &structs.Chats{ID: chat.ID, DB: db}
}

func resultSuccess(t *testing.T, m map[string]*any) bool {
	t.Helper()
	if m == nil {
		t.Fatal("expected result map")
	}
	ptr, ok := m["success"]
	if !ok || ptr == nil {
		t.Fatal("expected success field")
	}
	v, ok := (*ptr).(bool)
	if !ok {
		t.Fatal("success is not bool")
	}
	return v
}

func TestGoalContent(t *testing.T) {
	session := &structs.Chats{}
	if _, ok := GoalContent(session); ok {
		t.Fatal("empty goal should not be provided")
	}
	session.Goal = "ship the feature"
	content, ok := GoalContent(session)
	if !ok || !strings.Contains(content, "ship the feature") || !strings.HasPrefix(content, "<goal>") {
		t.Fatalf("unexpected goal content: %q ok=%v", content, ok)
	}
}

func TestWriteGoal_PassOtherPath(t *testing.T) {
	pass, _, result, _ := writeGoal(&structs.Chats{}, map[string]*any{
		"path": anyPtr("README.md"),
	}, nil)
	if !pass || result != nil {
		t.Fatalf("non-@goal path should pass through, pass=%v result=%v", pass, result)
	}
}

func TestWriteGoal_SetOnce(t *testing.T) {
	session := newTestSession(t)
	pass, _, result, err := writeGoal(session, map[string]*any{
		"path":   anyPtr("@goal"),
		"target": anyPtr("@all"),
		"text":   anyPtr("finish the implementation"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pass {
		t.Fatal("writeGoal should stop edit.writeFile")
	}
	if !resultSuccess(t, result) {
		t.Fatalf("expected success=true: %#v", result)
	}
	if session.Goal != "finish the implementation" {
		t.Fatalf("goal not set: %q", session.Goal)
	}
	var stored structs.Chats
	if err := session.DB.Select("goal").Where("id = ?", session.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Goal != session.Goal {
		t.Fatalf("goal not persisted: %q", stored.Goal)
	}

	// 第二次写入必须失败且不修改已有值。
	_, _, result, _ = writeGoal(session, map[string]*any{
		"path":   anyPtr("@goal"),
		"target": anyPtr("@all"),
		"text":   anyPtr("overwrite"),
	}, nil)
	if resultSuccess(t, result) {
		t.Fatal("second write must fail")
	}
	if session.Goal != "finish the implementation" {
		t.Fatalf("goal changed after rejected write: %q", session.Goal)
	}
}

func TestWriteGoal_TooLongAndEmpty(t *testing.T) {
	session := newTestSession(t)
	long := strings.Repeat("字", structs.MaxGoalRunes+1)
	_, _, result, _ := writeGoal(session, map[string]*any{
		"path":   anyPtr("@goal"),
		"target": anyPtr("@all"),
		"text":   anyPtr(long),
	}, nil)
	if resultSuccess(t, result) {
		t.Fatal("oversized goal must fail")
	}
	if session.Goal != "" {
		t.Fatal("oversized goal must not be persisted")
	}

	_, _, result, _ = writeGoal(session, map[string]*any{
		"path":   anyPtr("@goal"),
		"target": anyPtr("@all"),
		"text":   anyPtr("   "),
	}, nil)
	if resultSuccess(t, result) {
		t.Fatal("empty goal must fail")
	}
}
