package trace

import (
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// ---- 回归：@temp 不存在 / confirmed 内容路径键 ----
//
// 背景（DEEP_REVIEW 确认缺陷）：
//   - read 一个不存在的 @temp/... 路径时 GORM First 的错误被忽略，空内容被当成
//     读取成功，还会写脏 Traces 行并递增 TraceID；
//   - confirmed 内容用调用方原样传入的字符串作 key，read 记 "a.cs"、edit 用
//     "./a.cs" 就命中不到，外部修改保护被静默绕过。

func TestTraceMissingTempObjectReturnsError(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := structs.Chats{ID: 1, TraceID: 0}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}

	session := &structs.Chats{
		ID:                   1,
		DB:                   db,
		TemporyDataOfSession: make(map[string]any),
		NowAgent:             "test_agent",
	}

	path := any("@temp/does_not_exist.txt")
	pass, _, result, err := Trace(session, map[string]*any{"path": &path}, []*any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pass {
		t.Error("expected pass=false")
	}
	success, ok := (*result["success"]).(bool)
	if !ok || success {
		t.Fatalf("不存在的 @temp 对象必须读取失败, result=%#v", result)
	}
	if session.TraceID != 0 {
		t.Errorf("失败的 read 不应递增 TraceID, got %d", session.TraceID)
	}

	var count int64
	if err := db.Model(&structs.Traces{}).Where("chat_id = ?", session.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("失败的 read 不应写入 Traces 行, got %d", count)
	}
}

func TestForgetEditContent(t *testing.T) {
	session := &structs.Chats{}
	ConfirmEditContent(session, "a.txt", "agent\n")

	// 用不同写法清理，规范化后必须命中同一条记录
	ForgetEditContent(session, "./a.txt")
	if err := CheckEditContent(session, "a.txt", "external\n"); err != nil {
		t.Fatalf("清理后不应再命中确认记录: %v", err)
	}

	// 未确认过的路径清理是幂等的
	ForgetEditContent(session, "never-confirmed.txt")
}

func TestCheckEditContentNormalizesPathKeys(t *testing.T) {
	session := &structs.Chats{}
	ConfirmEditContent(session, "a.txt", "agent\n")
	if err := CheckEditContent(session, "./a.txt", "external\n"); err == nil {
		t.Fatal("同一路径的不同写法必须命中同一条确认记录（否则外部修改保护被静默绕过）")
	}

	session2 := &structs.Chats{}
	ConfirmEditContent(session2, "./b.txt", "agent\n")
	if err := CheckEditContent(session2, "b.txt", "external\n"); err == nil {
		t.Fatal("确认记录的写法不同时也必须命中")
	}
}
