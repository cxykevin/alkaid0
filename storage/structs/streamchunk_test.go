package structs

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupStreamTest 打开带 Chats/Messages/StreamChunk 的内存库。
// 内存库只有用同一条连接才能看到同一份数据，因此连接池限制为 1
// （与 storage.InitDB 的生产配置一致）。
func setupStreamTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&Chats{}, &Messages{}, &StreamChunk{}); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	return db
}

// streamTestMessage 建一条空的 assistant 消息（流式响应的占位行）。
func streamTestMessage(t *testing.T, db *gorm.DB, chatID uint32) *Messages {
	t.Helper()
	m := &Messages{ChatID: chatID, Type: MessagesRoleAgent}
	if err := AppendMessage(db, m); err != nil {
		t.Fatalf("创建 assistant 占位消息失败: %v", err)
	}
	return m
}

// streamTestChunks 读取某条消息残留的日志行（按写入顺序）。
func streamTestChunks(t *testing.T, db *gorm.DB, messageID uint64) []StreamChunk {
	t.Helper()
	var rows []StreamChunk
	if err := db.Where("message_id = ?", messageID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("读取 stream_chunks 失败: %v", err)
	}
	return rows
}

// streamTestJoin 把日志行按类型拼起来，返回 (正文, 思考)。
func streamTestJoin(rows []StreamChunk) (string, string) {
	var delta, thinking strings.Builder
	for _, r := range rows {
		if r.Thinking {
			thinking.WriteString(r.Content)
		} else {
			delta.WriteString(r.Content)
		}
	}
	return delta.String(), thinking.String()
}

// TestStreamJournalAppendsOnlyIncrementsAndFinalizeWritesContent 覆盖正常流式的
// 完整生命周期：内存攒够阈值才写日志、只写新增片段、收尾一次写入内容并清日志。
func TestStreamJournalAppendsOnlyIncrementsAndFinalizeWritesContent(t *testing.T) {
	db := setupStreamTest(t)
	branchTestChat(t, db, 1)
	msg := streamTestMessage(t, db, 1)

	j := NewStreamJournal(db, msg.ID, 4) // 阈值 4 字符，方便观察
	defer j.Close()

	if err := j.Write("ab", ""); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rows := streamTestChunks(t, db, msg.ID); len(rows) != 0 {
		t.Fatalf("未达阈值不应落库，got %d rows", len(rows))
	}

	// 正文 2 + 思考 2 = 4，达到阈值 → 一次写入两行（正文/思考各一行）
	if err := j.Write("cd", "TT"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	rows := streamTestChunks(t, db, msg.ID)
	if len(rows) != 2 {
		t.Fatalf("期望 2 行日志，got %d", len(rows))
	}
	if delta, thinking := streamTestJoin(rows); delta != "abcd" || thinking != "TT" {
		t.Fatalf("日志内容错误: delta=%q thinking=%q", delta, thinking)
	}

	// 流式中内容列保持为空：只在收尾时写一次（这是不再整列 UPDATE 的直接结果）
	var during Messages
	if err := db.First(&during, msg.ID).Error; err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	if during.Delta != "" || during.ThinkingDelta != "" {
		t.Fatalf("流式中不应写内容列: delta=%q thinking=%q", during.Delta, during.ThinkingDelta)
	}

	// 又来一段增量：只写这一段，不重写前面的
	if err := j.Write("ef", ""); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := j.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rows = streamTestChunks(t, db, msg.ID)
	if len(rows) != 3 {
		t.Fatalf("增量为 2 字符应新增 1 行（共 3 行），got %d", len(rows))
	}
	if rows[2].Content != "ef" {
		t.Fatalf("最后一行应只是新增片段 ef，got %q", rows[2].Content)
	}

	// 收尾：一次写入最终内容 + 清空日志（单事务）
	if err := FinalizeStreamMessage(db, msg.ID, &StreamFinal{
		Delta:         "abcdef",
		ThinkingDelta: "TT",
		Phase:         "final_answer",
		PromptTokens:  12,
	}); err != nil {
		t.Fatalf("FinalizeStreamMessage: %v", err)
	}
	var final Messages
	if err := db.First(&final, msg.ID).Error; err != nil {
		t.Fatalf("读取收尾后的消息失败: %v", err)
	}
	if final.Delta != "abcdef" || final.ThinkingDelta != "TT" {
		t.Fatalf("收尾内容错误: delta=%q thinking=%q", final.Delta, final.ThinkingDelta)
	}
	if final.Phase != "final_answer" || final.PromptTokens != 12 {
		t.Fatalf("phase/token 用量未写入: phase=%q prompt=%d", final.Phase, final.PromptTokens)
	}
	if rows := streamTestChunks(t, db, msg.ID); len(rows) != 0 {
		t.Fatalf("收尾后日志应清空，got %d rows", len(rows))
	}
}

// TestRecoverStreamChunksMergesLeftoversFromAbnormalExit 模拟「流式中被 kill」：
// 消息行内容为空、只有日志行，恢复时按写入顺序拼回正文/思考并清空日志。
func TestRecoverStreamChunksMergesLeftoversFromAbnormalExit(t *testing.T) {
	db := setupStreamTest(t)
	branchTestChat(t, db, 1)
	msg := streamTestMessage(t, db, 1)

	// 正文与思考交错写入，恢复后各自按写入顺序拼接
	rows := []StreamChunk{
		{MessageID: msg.ID, Content: "hello "},
		{MessageID: msg.ID, Thinking: true, Content: "让我看看"},
		{MessageID: msg.ID, Content: "world"},
		{MessageID: msg.ID, Thinking: true, Content: "……好了"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("写入日志行失败: %v", err)
	}

	n, err := RecoverStreamChunks(db)
	if err != nil {
		t.Fatalf("RecoverStreamChunks: %v", err)
	}
	if n != 1 {
		t.Fatalf("应恢复 1 条消息，got %d", n)
	}
	var got Messages
	if err := db.First(&got, msg.ID).Error; err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	if got.Delta != "hello world" {
		t.Fatalf("正文恢复错误: %q", got.Delta)
	}
	if got.ThinkingDelta != "让我看看……好了" {
		t.Fatalf("思考恢复错误: %q", got.ThinkingDelta)
	}
	if rows := streamTestChunks(t, db, msg.ID); len(rows) != 0 {
		t.Fatalf("恢复后日志应清空，got %d rows", len(rows))
	}

	// 幂等：再跑一次没有残留可恢复，内容不变
	n, err = RecoverStreamChunks(db)
	if err != nil {
		t.Fatalf("第二次 RecoverStreamChunks: %v", err)
	}
	if n != 0 {
		t.Fatalf("第二次不应再恢复任何消息，got %d", n)
	}
	if err := db.First(&got, msg.ID).Error; err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	if got.Delta != "hello world" {
		t.Fatalf("内容被重复追加: %q", got.Delta)
	}
}

// TestRecoverStreamChunksSkipsActiveStream 本进程正在进行的流式消息不能被当成
// 崩溃残留：同一个数据库文件在流式期间可能被再次打开（会话加载 / 后台 workflow）。
func TestRecoverStreamChunksSkipsActiveStream(t *testing.T) {
	db := setupStreamTest(t)
	branchTestChat(t, db, 1)
	msg := streamTestMessage(t, db, 1)
	if err := db.Create(&StreamChunk{MessageID: msg.ID, Content: "in flight"}).Error; err != nil {
		t.Fatalf("写入日志行失败: %v", err)
	}

	j := NewStreamJournal(db, msg.ID, 256) // 登记为活跃
	n, err := RecoverStreamChunks(db)
	if err != nil {
		t.Fatalf("RecoverStreamChunks: %v", err)
	}
	if n != 0 {
		t.Fatalf("进行中的流不应被恢复，got %d", n)
	}
	if rows := streamTestChunks(t, db, msg.ID); len(rows) != 1 {
		t.Fatalf("进行中的流日志应保留，got %d rows", len(rows))
	}

	// 收尾（或请求结束）后才会被当作残留处理
	j.Close()
	if n, err := RecoverStreamChunks(db); err != nil || n != 1 {
		t.Fatalf("注销后应恢复 1 条: n=%d err=%v", n, err)
	}
}

// TestRecoverStreamChunksDropsOrphans 消息已被删除（占位行清理/会话被删）时，
// 残留日志直接丢弃，不应报错。
func TestRecoverStreamChunksDropsOrphans(t *testing.T) {
	db := setupStreamTest(t)
	if err := db.Create(&StreamChunk{MessageID: 9999, Content: "orphan"}).Error; err != nil {
		t.Fatalf("写入日志行失败: %v", err)
	}

	n, err := RecoverStreamChunks(db)
	if err != nil {
		t.Fatalf("RecoverStreamChunks: %v", err)
	}
	if n != 1 {
		t.Fatalf("孤立日志也应被处理（计入清理条数），got %d", n)
	}
	if rows := streamTestChunks(t, db, 9999); len(rows) != 0 {
		t.Fatalf("孤立日志应被删除，got %d rows", len(rows))
	}
}

// TestDeleteMessageRemovesStreamChunks 删除消息时一并清掉它的流式日志。
func TestDeleteMessageRemovesStreamChunks(t *testing.T) {
	db := setupStreamTest(t)
	branchTestChat(t, db, 1)
	msg := streamTestMessage(t, db, 1)
	if err := db.Create(&StreamChunk{MessageID: msg.ID, Content: "x"}).Error; err != nil {
		t.Fatalf("写入日志行失败: %v", err)
	}

	if err := DeleteMessage(db, msg.ID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if rows := streamTestChunks(t, db, msg.ID); len(rows) != 0 {
		t.Fatalf("删除消息后日志应清空，got %d rows", len(rows))
	}
}
