package structs

import (
	"slices"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupBranchTest 打开带 Chats/Messages 表的内存库（与生产相同的 glebarez/sqlite 驱动）。
func setupBranchTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&Chats{}, &Messages{}); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	return db
}

// branchTestChat 建一个会话行。
func branchTestChat(t *testing.T, db *gorm.DB, id uint32) {
	t.Helper()
	if err := db.Create(&Chats{ID: id, LastModelID: 1}).Error; err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
}

// branchTestAppend 用 AppendMessage 追加一条用户消息。
func branchTestAppend(t *testing.T, db *gorm.DB, chatID uint32, delta string) *Messages {
	t.Helper()
	m := &Messages{ChatID: chatID, Delta: delta, Type: MessagesRoleUser}
	if err := AppendMessage(db, m); err != nil {
		t.Fatalf("AppendMessage(%q) 失败: %v", delta, err)
	}
	return m
}

// branchTestIDs 查询活跃分支上的消息 id（升序）。
func branchTestIDs(t *testing.T, db *gorm.DB, chatID uint32) []uint64 {
	t.Helper()
	var msgs []Messages
	if err := OnActiveBranch(db, chatID).Order("id ASC").Find(&msgs).Error; err != nil {
		t.Fatalf("查询活跃分支失败: %v", err)
	}
	ids := make([]uint64, 0, len(msgs))
	for i := range msgs {
		ids = append(ids, msgs[i].ID)
	}
	return ids
}

// branchTestLeaf 读取会话当前叶子。
func branchTestLeaf(t *testing.T, db *gorm.DB, chatID uint32) *uint64 {
	t.Helper()
	var chat Chats
	if err := db.Select("id", "active_leaf_id").First(&chat, chatID).Error; err != nil {
		t.Fatalf("读取会话失败: %v", err)
	}
	return chat.ActiveLeafID
}

// branchTestAssertIDs 断言 id 序列完全一致。
func branchTestAssertIDs(t *testing.T, got []uint64, want []uint64) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("活跃分支 id = %v, 期望 %v", got, want)
	}
}

// TestOnActiveBranchFallback 验证无叶会话（未回填历史库 / 直接 Create 的线性消息）退回全量扫描。
func TestOnActiveBranchFallback(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	// 场景1：会话存在但 active_leaf_id 为 NULL，消息直接 Create（无 parent）
	var msgs []*Messages
	for i, delta := range []string{"a", "b", "c"} {
		m := &Messages{ChatID: 1, Delta: delta, Type: MessagesRoleUser}
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("直接创建第 %d 条消息失败: %v", i, err)
		}
		msgs = append(msgs, m)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{msgs[0].ID, msgs[1].ID, msgs[2].ID})

	// 场景2：chats 行不存在（游离消息）同样全量返回
	orphan := &Messages{ChatID: 2, Delta: "orphan", Type: MessagesRoleUser}
	if err := db.Create(orphan).Error; err != nil {
		t.Fatalf("创建游离消息失败: %v", err)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 2), []uint64{orphan.ID})
}

// TestAppendMessageBuildsChain 验证空会话上追加消息会建立链并推进叶子。
func TestAppendMessageBuildsChain(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	m1 := branchTestAppend(t, db, 1, "m1")
	m2 := branchTestAppend(t, db, 1, "m2")
	m3 := branchTestAppend(t, db, 1, "m3")

	if m1.ParentID != nil {
		t.Fatalf("首条消息应为根（parent=NULL），得到 %v", *m1.ParentID)
	}
	if m2.ParentID == nil || *m2.ParentID != m1.ID {
		t.Fatalf("m2.ParentID 应为 %d，得到 %v", m1.ID, m2.ParentID)
	}
	if m3.ParentID == nil || *m3.ParentID != m2.ID {
		t.Fatalf("m3.ParentID 应为 %d，得到 %v", m2.ID, m3.ParentID)
	}

	leaf := branchTestLeaf(t, db, 1)
	if leaf == nil || *leaf != m3.ID {
		t.Fatalf("叶子应为 %d，得到 %v", m3.ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{m1.ID, m2.ID, m3.ID})
}

// TestAppendMessageLocalBackfill 验证「会话有历史消息但没有叶子」时先局部回填再挂载。
func TestAppendMessageLocalBackfill(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	// 模拟旧库/测试直接写入的线性消息：parent 全空、leaf 为空
	old1 := &Messages{ChatID: 1, Delta: "old1", Type: MessagesRoleUser}
	old2 := &Messages{ChatID: 1, Delta: "old2", Type: MessagesRoleAgent}
	if err := db.Create(old1).Error; err != nil {
		t.Fatalf("创建旧消息 1 失败: %v", err)
	}
	if err := db.Create(old2).Error; err != nil {
		t.Fatalf("创建旧消息 2 失败: %v", err)
	}

	m3 := branchTestAppend(t, db, 1, "m3")

	if m3.ParentID == nil || *m3.ParentID != old2.ID {
		t.Fatalf("新消息 parent 应为回填后的叶子 %d，得到 %v", old2.ID, m3.ParentID)
	}
	// 回填结果：old1 为根，old2 挂在 old1 上
	var reloaded Messages
	if err := db.Select("id", "parent_id").First(&reloaded, old1.ID).Error; err != nil {
		t.Fatalf("读取旧消息 1 失败: %v", err)
	}
	if reloaded.ParentID != nil {
		t.Fatalf("回填后 old1 应为根，得到 parent=%v", *reloaded.ParentID)
	}
	var reloadedOld2 Messages
	if err := db.Select("id", "parent_id").First(&reloadedOld2, old2.ID).Error; err != nil {
		t.Fatalf("读取旧消息 2 失败: %v", err)
	}
	if reloadedOld2.ParentID == nil || *reloadedOld2.ParentID != old1.ID {
		t.Fatalf("回填后 old2.parent 应为 %d，得到 %v", old1.ID, reloadedOld2.ParentID)
	}

	leaf := branchTestLeaf(t, db, 1)
	if leaf == nil || *leaf != m3.ID {
		t.Fatalf("叶子应为 %d，得到 %v", m3.ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{old1.ID, old2.ID, m3.ID})
}

// TestAppendMessageNoChatRow 验证 chats 行缺失时追加退化为纯写入。
func TestAppendMessageNoChatRow(t *testing.T) {
	db := setupBranchTest(t)

	m := &Messages{ChatID: 7, Delta: "no-chat", Type: MessagesRoleUser}
	if err := AppendMessage(db, m); err != nil {
		t.Fatalf("AppendMessage 失败: %v", err)
	}
	if m.ParentID != nil {
		t.Fatalf("无会话行时 parent 应为 NULL，得到 %v", *m.ParentID)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 7), []uint64{m.ID})
}

// TestActiveBranchFork 验证分叉后只有活跃分支（叶子回溯链）被读到，旁支消息被隔离。
func TestActiveBranchFork(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	m1 := branchTestAppend(t, db, 1, "m1")
	m2 := branchTestAppend(t, db, 1, "m2")
	branchTestAppend(t, db, 1, "m3")
	branchTestAppend(t, db, 1, "m4")

	// 从 m2 分叉出新分支并切换活跃叶子到分叉点
	fork := &Messages{ChatID: 1, ParentID: &m2.ID, Delta: "fork", Type: MessagesRoleUser}
	if err := db.Create(fork).Error; err != nil {
		t.Fatalf("创建分叉消息失败: %v", err)
	}
	if err := db.Model(&Chats{}).Where("id = ?", 1).Update("active_leaf_id", fork.ID).Error; err != nil {
		t.Fatalf("切换活跃叶子失败: %v", err)
	}

	// 活跃分支 = m1 → m2 → fork；m3/m4 属于旁支不应出现
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{m1.ID, m2.ID, fork.ID})

	// 旁支消息仍在表内（供 rewind 回到旧分支）
	var total int64
	if err := db.Model(&Messages{}).Where("chat_id = ?", 1).Count(&total).Error; err != nil {
		t.Fatalf("统计消息总数失败: %v", err)
	}
	if total != 5 {
		t.Fatalf("消息总数应为 5（含旁支），得到 %d", total)
	}
}

// TestOnActiveBranchChainable 验证限定条件可与 Model/Where/Count/分页自由组合。
func TestOnActiveBranchChainable(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	for i := 0; i < 5; i++ {
		branchTestAppend(t, db, 1, "m")
	}

	// 继续链式 Where + Count
	var n int64
	if err := OnActiveBranch(db.Model(&Messages{}).Where("type = ?", MessagesRoleUser), 1).
		Count(&n).Error; err != nil {
		t.Fatalf("Count 失败: %v", err)
	}
	if n != 5 {
		t.Fatalf("活跃分支用户消息数应为 5，得到 %d", n)
	}

	// 分页：倒序第二页
	var page []Messages
	if err := OnActiveBranch(db, 1).Order("id DESC").Offset(1).Limit(2).Find(&page).Error; err != nil {
		t.Fatalf("分页查询失败: %v", err)
	}
	if len(page) != 2 || page[0].Delta != "m" {
		t.Fatalf("分页结果异常: 长度 %d，首条 %q", len(page), page[0].Delta)
	}
	// 倒序第 2、3 条应为 id 第 4、3 序号的消息
	var all []Messages
	if err := OnActiveBranch(db, 1).Order("id ASC").Find(&all).Error; err != nil {
		t.Fatalf("全量查询失败: %v", err)
	}
	if page[0].ID != all[3].ID || page[1].ID != all[2].ID {
		t.Fatalf("分页 id 应为 [%d %d]，得到 [%d %d]", all[3].ID, all[2].ID, page[0].ID, page[1].ID)
	}
}

// TestDeleteMessageLeafRewind 验证删除叶子时叶子回退到父节点。
func TestDeleteMessageLeafRewind(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	m1 := branchTestAppend(t, db, 1, "m1")
	m2 := branchTestAppend(t, db, 1, "m2")
	m3 := branchTestAppend(t, db, 1, "m3")

	if err := DeleteMessage(db, m3.ID); err != nil {
		t.Fatalf("删除叶子失败: %v", err)
	}
	leaf := branchTestLeaf(t, db, 1)
	if leaf == nil || *leaf != m2.ID {
		t.Fatalf("删除叶子后应回退到 %d，得到 %v", m2.ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{m1.ID, m2.ID})
}

// TestDeleteMessageMiddleRewire 验证删除中间节点时子节点上提、链保持连续。
func TestDeleteMessageMiddleRewire(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	m1 := branchTestAppend(t, db, 1, "m1")
	m2 := branchTestAppend(t, db, 1, "m2")
	m3 := branchTestAppend(t, db, 1, "m3")
	m4 := branchTestAppend(t, db, 1, "m4")

	if err := DeleteMessage(db, m2.ID); err != nil {
		t.Fatalf("删除中间节点失败: %v", err)
	}
	var reloaded Messages
	if err := db.Select("id", "parent_id").First(&reloaded, m3.ID).Error; err != nil {
		t.Fatalf("读取子节点失败: %v", err)
	}
	if reloaded.ParentID == nil || *reloaded.ParentID != m1.ID {
		t.Fatalf("子节点应上提到 %d，得到 %v", m1.ID, reloaded.ParentID)
	}
	// 活跃叶子不变，分支按新链连续
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != m4.ID {
		t.Fatalf("叶子不应变化，期望 %d，得到 %v", m4.ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{m1.ID, m3.ID, m4.ID})
}

// TestDeleteMessageToEmpty 验证删到空会话时叶子回退为 NULL、分支查询回到回退模式。
func TestDeleteMessageToEmpty(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	m1 := branchTestAppend(t, db, 1, "m1")
	m2 := branchTestAppend(t, db, 1, "m2")

	if err := DeleteMessage(db, m2.ID); err != nil {
		t.Fatalf("删除叶子失败: %v", err)
	}
	if err := DeleteMessage(db, m1.ID); err != nil {
		t.Fatalf("删除最后一条失败: %v", err)
	}
	if leaf := branchTestLeaf(t, db, 1); leaf != nil {
		t.Fatalf("空会话叶子应为 NULL，得到 %v", *leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{})
}

// TestDeleteMessageIdempotent 验证删除不存在的消息不报错、不影响现有树。
func TestDeleteMessageIdempotent(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	m1 := branchTestAppend(t, db, 1, "m1")

	if err := DeleteMessage(db, 999999); err != nil {
		t.Fatalf("删除不存在消息应无错误，得到 %v", err)
	}
	if err := DeleteMessage(db, m1.ID); err != nil {
		t.Fatalf("首次删除失败: %v", err)
	}
	if err := DeleteMessage(db, m1.ID); err != nil {
		t.Fatalf("重复删除应无错误，得到 %v", err)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{})
}
