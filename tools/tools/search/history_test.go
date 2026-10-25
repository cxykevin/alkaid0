package search

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupHistoryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&structs.Chats{}, &structs.Messages{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedChat(t *testing.T, db *gorm.DB, id uint32, title, aiTitle string, messages ...string) {
	t.Helper()
	if err := db.Create(&structs.Chats{ID: id, Title: title, AITitle: aiTitle}).Error; err != nil {
		t.Fatalf("create chat %d: %v", id, err)
	}
	for i, m := range messages {
		role := structs.MessagesRoleUser
		if i%2 == 1 {
			role = structs.MessagesRoleAgent
		}
		if err := db.Create(&structs.Messages{ChatID: id, Type: role, Delta: m}).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}
}

func TestSearchHistoryByTitleContentAndID(t *testing.T) {
	db := setupHistoryDB(t)

	seedChat(t, db, 100, "cronjob design", "", "let's build a cronjob system", "done")
	seedChat(t, db, 101, "", "API 讨论", "rate limit handling")
	seedChat(t, db, 102, "current session", "", "cronjob notes in current session")

	// 标题 + 内容命中
	m, err := searchHistorySessions(db, "cronjob", 0, 102, 10)
	if err != nil {
		t.Fatalf("searchHistorySessions: %v", err)
	}
	if len(m) != 1 || m[0].ChatID != 100 {
		t.Fatalf("应只命中会话 100（当前会话 102 必须排除），got %+v", m)
	}
	if !m[0].MatchedTitle || len(m[0].Snippets) == 0 {
		t.Errorf("应同时报告标题与内容命中: %+v", m[0])
	}
	if !strings.Contains(m[0].Snippets[0].Text, "cronjob") {
		t.Errorf("片段应包含命中词: %q", m[0].Snippets[0].Text)
	}

	// AI 标题命中
	m, err = searchHistorySessions(db, "API", 0, 102, 10)
	if err != nil {
		t.Fatalf("searchHistorySessions: %v", err)
	}
	if len(m) != 1 || m[0].ChatID != 101 || !m[0].MatchedTitle {
		t.Fatalf("应命中会话 101 的 AI 标题: %+v", m)
	}

	// 仅内容命中
	m, err = searchHistorySessions(db, "rate", 0, 102, 10)
	if err != nil {
		t.Fatalf("searchHistorySessions: %v", err)
	}
	if len(m) != 1 || m[0].ChatID != 101 || len(m[0].Snippets) == 0 {
		t.Fatalf("应命中会话 101 的消息内容: %+v", m)
	}

	// session id
	m, err = searchHistorySessions(db, "101", 0, 102, 10)
	if err != nil {
		t.Fatalf("searchHistorySessions: %v", err)
	}
	if len(m) != 1 || m[0].ChatID != 101 || !m[0].MatchedID {
		t.Fatalf("数字 query 应命中 session id: %+v", m)
	}
}

func TestSearchHistoryScoped(t *testing.T) {
	db := setupHistoryDB(t)

	seedChat(t, db, 100, "one", "", "shared keyword here")
	seedChat(t, db, 101, "two", "", "shared keyword there")
	seedChat(t, db, 102, "current", "", "nothing")

	m, err := searchHistorySessions(db, "shared", 100, 102, 10)
	if err != nil {
		t.Fatalf("searchHistorySessions: %v", err)
	}
	if len(m) != 1 || m[0].ChatID != 100 {
		t.Fatalf("path=@history/100 只能命中会话 100: %+v", m)
	}

	// 无命中
	m, err = searchHistorySessions(db, "not-present-anywhere", 0, 102, 10)
	if err != nil {
		t.Fatalf("searchHistorySessions: %v", err)
	}
	if len(m) != 0 {
		t.Fatalf("不应有命中: %+v", m)
	}
}

func TestSearchHistoryLIKEWildcardsAreLiteral(t *testing.T) {
	db := setupHistoryDB(t)
	seedChat(t, db, 100, "literal", "", "100% done")
	seedChat(t, db, 101, "other", "", "plain text")

	m, err := searchHistorySessions(db, "100%", 0, 102, 10)
	if err != nil {
		t.Fatalf("searchHistorySessions: %v", err)
	}
	if len(m) != 1 || m[0].ChatID != 100 {
		t.Fatalf("%% 必须按字面匹配: %+v", m)
	}
}

func TestSearchTypeParameter(t *testing.T) {
	if _, ok := paras["type"]; !ok {
		t.Fatal("type 参数未注册")
	}
	if _, ok := paras["online"]; ok {
		t.Fatal("online 参数应已被 type 取代")
	}
	mk := func(v any) map[string]*any { return map[string]*any{"type": &v} }
	if mode, err := getSearchType(mk(" Local ")); err != nil || mode != searchModeLocal {
		t.Fatalf("local 应被接受并归一化: mode=%q err=%v", mode, err)
	}
	if mode, err := getSearchType(mk("history")); err != nil || mode != searchModeHistory {
		t.Fatalf("history 应被接受: mode=%q err=%v", mode, err)
	}
	if _, err := getSearchType(mk("web")); err == nil {
		t.Fatal("未知 type 必须报错")
	}
	if _, err := getSearchType(map[string]*any{}); err == nil {
		t.Fatal("缺少 type 必须报错")
	}
}

func TestRunHistorySearchOutput(t *testing.T) {
	db := setupHistoryDB(t)
	seedChat(t, db, 100, "cronjob design", "", "let's build a cronjob system")

	session := &structs.Chats{ID: 102, DB: db}
	query := any("cronjob")
	typ := any("history")
	mp := map[string]*any{"query": &query, "type": &typ}

	_, _, result, err := runHistorySearch(session, mp, nil, "cronjob")
	if err != nil {
		t.Fatalf("runHistorySearch: %v", err)
	}
	if success, ok := (*result["success"]).(bool); !ok || !success {
		t.Fatal("runHistorySearch 应成功")
	}
	out, _ := (*result["output"]).(string)
	if !strings.Contains(out, "@history/100") && !strings.Contains(out, "[session 100]") {
		t.Errorf("输出应包含会话 id 与 read 提示: %q", out)
	}
	if !strings.Contains(out, "Use read with path") {
		t.Errorf("输出应提示用 read 读取历史会话: %q", out)
	}
}
