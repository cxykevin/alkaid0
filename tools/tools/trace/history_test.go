package trace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// seedHistoryMessages 创建会话 id 并写入 n 条「用户 / AI」交替消息。
func seedHistoryMessages(t *testing.T, db *gorm.DB, id uint32, title string, n int) {
	t.Helper()
	if err := db.Create(&structs.Chats{ID: id, Title: title}).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	for i := range n {
		role := structs.MessagesRoleUser
		if i%2 == 1 {
			role = structs.MessagesRoleAgent
		}
		if err := db.Create(&structs.Messages{
			ChatID: id,
			Type:   role,
			Delta:  fmt.Sprintf("msg-%02d", i),
		}).Error; err != nil {
			t.Fatalf("create message #%d: %v", i, err)
		}
	}
}

func TestHistorySessionContentLast16(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	seedHistoryMessages(t, db, 7, "cron work", 20)
	// 工具消息不计入历史会话内容
	if err := db.Create(&structs.Messages{ChatID: 7, Type: structs.MessagesRoleTool, Delta: "tool-payload"}).Error; err != nil {
		t.Fatalf("create tool message: %v", err)
	}

	session := &structs.Chats{ID: 1, DB: db, Root: t.TempDir()}
	content, err := historySessionContent(session, "@history/7")
	if err != nil {
		t.Fatalf("historySessionContent: %v", err)
	}
	if !strings.Contains(content, "[session 7] cron work") {
		t.Errorf("缺少会话头: %q", content[:min(len(content), 80)])
	}
	// 最后 16 条 = msg-04..msg-19
	if strings.Contains(content, "msg-03") || strings.Contains(content, "msg-00") {
		t.Error("超过 16 条的旧消息不应出现")
	}
	if !strings.Contains(content, "msg-04") || !strings.Contains(content, "msg-19") {
		t.Error("必须包含窗口内的最早与最新消息")
	}
	if strings.Contains(content, "tool-payload") {
		t.Error("工具消息不计入历史会话内容")
	}
	if strings.Index(content, "msg-04") > strings.Index(content, "msg-19") {
		t.Error("消息必须按时间正序渲染")
	}
	if !strings.Contains(content, "[User #") || !strings.Contains(content, "[Assistant #") {
		t.Error("角色头必须带消息 id")
	}
}

func TestHistorySessionContentErrors(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	session := &structs.Chats{ID: 1, DB: db, Root: t.TempDir()}

	if _, err := historySessionContent(session, "@history/notanumber"); err == nil {
		t.Error("非法路径必须报错")
	}
	if _, err := historySessionContent(session, "@history/999"); err == nil {
		t.Error("不存在的会话必须报错")
	}
}

func TestHistoryIncrementSkipsRolledOutMessages(t *testing.T) {
	oldContent := "[session 1] t\n\n[User #1]\nhello\n\n[Assistant #2]\nworld\n"
	newContent := "[session 1] t\n\n[Assistant #2]\nworld\n\n[User #3]\nnext\n"

	inc, ok := historyIncrement(oldContent, newContent)
	if !ok {
		t.Fatal("应能找到窗口重叠并产出增量")
	}
	if strings.Contains(inc, "hello") {
		t.Errorf("滚出窗口的消息不得出现在增量里: %q", inc)
	}
	if strings.Contains(inc, "\n-") || strings.HasPrefix(inc, "-") {
		t.Errorf("增量不得包含 unified diff 删除行: %q", inc)
	}
	if !strings.Contains(inc, "[User #3]") || !strings.Contains(inc, "next") {
		t.Errorf("增量必须包含新增消息: %q", inc)
	}
}

func TestHistoryIncrementNoOverlap(t *testing.T) {
	oldContent := "[session 1] t\n\n[User #1]\nhello\n"
	newContent := "[session 1] t\n\n[User #3]\nnext\n"
	if _, ok := historyIncrement(oldContent, newContent); ok {
		t.Fatal("窗口被完全换掉时不应产出增量（退回完整内容块）")
	}
}

func TestRenderTraceBlocksHistoryDiffTail(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	seedHistoryMessages(t, db, 7, "other session", 16)
	session := &structs.Chats{
		ID:                   1,
		DB:                   db,
		NowAgent:             "test_agent",
		Root:                 t.TempDir(),
		TemporyDataOfSession: map[string]any{},
	}
	path := structs.HistoryPath(7)
	oldContent, err := historySessionContent(session, path)
	if err != nil {
		t.Fatalf("historySessionContent: %v", err)
	}
	if err := db.Create(&structs.Traces{
		ChatID: 1, Path: path, AgentID: "test_agent", TraceID: 3,
		LastContent: oldContent, AnchorMsgID: 50,
	}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}

	// read 事件停在原注入位置（事件 MsgID == AnchorMsgID）：历史会话是例外，
	// 不能因为"旧块与增量同锚点"退回整块，必须走 AnchorDiffTail。
	session.TemporyDataOfSession[structs.TempKeyTraceEvents] = map[string]*structs.TraceEvent{
		path: {MsgID: 50, ToolCallID: "call_history"},
	}

	// 目标会话继续对话：窗口滚动，两条旧消息被挤出
	for i := 16; i < 18; i++ {
		role := structs.MessagesRoleUser
		if i%2 == 1 {
			role = structs.MessagesRoleAgent
		}
		if err := db.Create(&structs.Messages{ChatID: 7, Type: role, Delta: fmt.Sprintf("msg-%02d", i)}).Error; err != nil {
			t.Fatalf("create message #%d: %v", i, err)
		}
	}

	_, blocks, err := RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}
	frag, ok := blocks[path]
	if !ok {
		t.Fatal("历史会话必须进 eventBlocks")
	}
	if frag.Text == oldContent {
		t.Fatal("内容已变化，不应复用旧块")
	}
	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	p := plans[path]
	if p == nil || p.Mode != AnchorDiffTail || p.PrevMsgID != 50 {
		t.Fatalf("历史会话应走「旧块留原位 + 增量追加末尾」: %+v", p)
	}
	diffPlans, _ := session.TemporyDataOfSession[structs.TempKeyTraceDiffPlan].(map[string]DiffPlan)
	dp := diffPlans[path]
	if !dp.Keep {
		t.Fatal("应产出方案2 候选（Keep=true）")
	}
	if dp.DiffBlock.Type != "update" {
		t.Errorf("历史会话增量块类型应为 update，got %q", dp.DiffBlock.Type)
	}
	if strings.Contains(dp.DiffBlock.Text, "msg-00") || strings.Contains(dp.DiffBlock.Text, "msg-01") {
		t.Errorf("滚出窗口的消息不得写进增量: %q", dp.DiffBlock.Text)
	}
	if !strings.Contains(dp.DiffBlock.Text, "msg-16") || !strings.Contains(dp.DiffBlock.Text, "msg-17") {
		t.Errorf("增量必须包含新消息: %q", dp.DiffBlock.Text)
	}
}

func TestTraceHistoryReadAndUnread(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	seedHistoryMessages(t, db, 7, "other session", 2)
	session := &structs.Chats{
		ID:                   1,
		DB:                   db,
		NowAgent:             "test_agent",
		Root:                 t.TempDir(),
		TemporyDataOfSession: map[string]any{},
	}
	path := structs.HistoryPath(7)
	mp := map[string]*any{"path": func() *any { v := any(path); return &v }()}

	pass, _, result, err := Trace(session, mp, nil)
	if err != nil {
		t.Fatalf("Trace: %v", err)
	}
	if pass {
		t.Error("Trace 成功时应返回 handled=false")
	}
	if success, ok := (*result["success"]).(bool); !ok || !success {
		errMsg := ""
		if p, ok := result["error"]; ok && p != nil {
			errMsg, _ = (*p).(string)
		}
		t.Fatalf("读取历史会话失败: %s", errMsg)
	}

	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = ?", path).First(&tr).Error; err != nil {
		t.Fatalf("历史会话必须写入 trace 行: %v", err)
	}
	if !strings.Contains(tr.LastContent, "msg-00") || !strings.Contains(tr.LastContent, "msg-01") {
		t.Errorf("trace 行应保存实时内容: %q", tr.LastContent)
	}
	content, ok := readTraceFileContent(session, "", tr)
	if !ok || content != tr.LastContent {
		t.Errorf("trace 内容应由历史会话提供: ok=%v", ok)
	}

	// unread 删除 trace 行
	mp["unread"] = func() *any { v := any(true); return &v }()
	if _, _, result, err = Trace(session, mp, nil); err != nil {
		t.Fatalf("Trace unread: %v", err)
	}
	if success, ok := (*result["success"]).(bool); !ok || !success {
		t.Fatal("unread 应成功")
	}
	var count int64
	if err := db.Model(&structs.Traces{}).Where("chat_id = 1 AND path = ?", path).Count(&count).Error; err != nil {
		t.Fatalf("count traces: %v", err)
	}
	if count != 0 {
		t.Errorf("unread 后 trace 行应被删除，got %d", count)
	}
}

func TestTraceHistoryMissingSession(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	session := &structs.Chats{
		ID:                   1,
		DB:                   db,
		NowAgent:             "test_agent",
		Root:                 t.TempDir(),
		TemporyDataOfSession: map[string]any{},
	}
	mp := map[string]*any{"path": func() *any { v := any("@history/999"); return &v }()}
	if _, _, result, err := Trace(session, mp, nil); err != nil {
		t.Fatalf("Trace: %v", err)
	} else if success, ok := (*result["success"]).(bool); !ok || success {
		t.Error("不存在的会话必须返回失败")
	}
}
