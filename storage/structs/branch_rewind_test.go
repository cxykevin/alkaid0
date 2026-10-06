package structs

import (
	"fmt"
	"testing"

	"gorm.io/gorm"
)

// 本文件覆盖消息树 rewind（RewindTo）的语义：
// 会话头指针移动、非删除语义、旁支保留、rewind 后追加分叉、
// 异常状态（有消息无叶子）补链修复、参数/目标校验与失败原子性、
// 跨会话隔离、以及与 DeleteMessage 的衔接。

// rewindTestChain 在指定会话上追加 n 条线性消息（m1..mN），返回它们。
func rewindTestChain(t *testing.T, db *gorm.DB, chatID uint32, n int) []*Messages {
	t.Helper()
	msgs := make([]*Messages, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, branchTestAppend(t, db, chatID, fmt.Sprintf("m%d", i+1)))
	}
	return msgs
}

// rewindTestAssertParent 断言消息的 parent_id（nil 表示根）。
func rewindTestAssertParent(t *testing.T, db *gorm.DB, msgID uint64, want *uint64) {
	t.Helper()
	var m Messages
	if err := db.Select("id", "parent_id").First(&m, msgID).Error; err != nil {
		t.Fatalf("读取消息 %d 失败: %v", msgID, err)
	}
	if (m.ParentID == nil) != (want == nil) {
		t.Fatalf("消息 %d 的 parent 应为 %v，得到 %v", msgID, want, m.ParentID)
	}
	if want != nil && *m.ParentID != *want {
		t.Fatalf("消息 %d 的 parent 应为 %d，得到 %d", msgID, *want, *m.ParentID)
	}
}

// rewindTestCount 统计会话内消息总数。
func rewindTestCount(t *testing.T, db *gorm.DB, chatID uint32) int64 {
	t.Helper()
	var total int64
	if err := db.Model(&Messages{}).Where("chat_id = ?", chatID).Count(&total).Error; err != nil {
		t.Fatalf("统计消息总数失败: %v", err)
	}
	return total
}

// TestRewindToMiddleMessage 验证常规链上回退：叶子移动、活跃分支收窄、消息全部保留。
func TestRewindToMiddleMessage(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	ms := rewindTestChain(t, db, 1, 4)

	if err := RewindTo(db, 1, ms[1].ID); err != nil {
		t.Fatalf("RewindTo 失败: %v", err)
	}
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != ms[1].ID {
		t.Fatalf("叶子应移动到 %d，得到 %v", ms[1].ID, leaf)
	}
	// 活跃分支 = m1 → m2；m3/m4 成为旁支
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID, ms[1].ID})
	// rewind 不删除任何消息：总数不变
	if total := rewindTestCount(t, db, 1); total != 4 {
		t.Fatalf("rewind 不应删除消息，总数应为 4，得到 %d", total)
	}
}

// TestRewindToRootMessage 验证回退到根消息（会话头移动到最早位置）。
func TestRewindToRootMessage(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	ms := rewindTestChain(t, db, 1, 3)

	if err := RewindTo(db, 1, ms[0].ID); err != nil {
		t.Fatalf("RewindTo 失败: %v", err)
	}
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != ms[0].ID {
		t.Fatalf("叶子应移动到根 %d，得到 %v", ms[0].ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID})
}

// TestRewindToCurrentHead 验证目标即当前会话头时幂等成功、状态不变。
func TestRewindToCurrentHead(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	ms := rewindTestChain(t, db, 1, 2)

	if err := RewindTo(db, 1, ms[1].ID); err != nil {
		t.Fatalf("回退到当前叶子应幂等成功: %v", err)
	}
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != ms[1].ID {
		t.Fatalf("叶子应保持 %d，得到 %v", ms[1].ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID, ms[1].ID})
}

// TestRewindBackToOldBranch 验证分叉后可以把会话头移动回原来的位置（旧分支），
// 并且两条分支之间可以来回切换。
func TestRewindBackToOldBranch(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	ms := rewindTestChain(t, db, 1, 4)

	// 从 m2 分叉：新分支 = m1 → m2 → fork
	fork := &Messages{ChatID: 1, ParentID: &ms[1].ID, Delta: "fork", Type: MessagesRoleUser}
	if err := db.Create(fork).Error; err != nil {
		t.Fatalf("创建分叉消息失败: %v", err)
	}
	if err := db.Model(&Chats{}).Where("id = ?", 1).Update("active_leaf_id", fork.ID).Error; err != nil {
		t.Fatalf("切换活跃叶子失败: %v", err)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID, ms[1].ID, fork.ID})

	// 会话头移动回旧分支末端 m4
	if err := RewindTo(db, 1, ms[3].ID); err != nil {
		t.Fatalf("RewindTo 回旧分支失败: %v", err)
	}
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != ms[3].ID {
		t.Fatalf("叶子应回到 %d，得到 %v", ms[3].ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID, ms[1].ID, ms[2].ID, ms[3].ID})

	// 再切回分叉分支，验证可往复
	if err := RewindTo(db, 1, fork.ID); err != nil {
		t.Fatalf("RewindTo 回分叉失败: %v", err)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID, ms[1].ID, fork.ID})
}

// TestRewindThenAppendFork 验证 rewind 后继续追加会从新的会话头处分叉，
// 旧分支消息不被触碰、仍可回退。
func TestRewindThenAppendFork(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	ms := rewindTestChain(t, db, 1, 4)

	if err := RewindTo(db, 1, ms[1].ID); err != nil {
		t.Fatalf("RewindTo 失败: %v", err)
	}
	added := branchTestAppend(t, db, 1, "m5")
	if added.ParentID == nil || *added.ParentID != ms[1].ID {
		t.Fatalf("分叉消息父节点应为 %d，得到 %v", ms[1].ID, added.ParentID)
	}
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != added.ID {
		t.Fatalf("叶子应为 %d，得到 %v", added.ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID, ms[1].ID, added.ID})

	// 旧分支 m3/m4 完好，可再回退过去
	if err := RewindTo(db, 1, ms[3].ID); err != nil {
		t.Fatalf("回退到旧分支失败: %v", err)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID, ms[1].ID, ms[2].ID, ms[3].ID})
}

// TestRewindRepairsUnmanagedState 验证「有历史消息但没有叶子」的异常状态下
// rewind 会先补链再移动会话头，早于目标的既有历史不会消失。
func TestRewindRepairsUnmanagedState(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	// 直接 Create 的线性消息：无 parent、无叶子（读取时为全量回退模式）
	old1 := &Messages{ChatID: 1, Delta: "old1", Type: MessagesRoleUser}
	old2 := &Messages{ChatID: 1, Delta: "old2", Type: MessagesRoleAgent}
	old3 := &Messages{ChatID: 1, Delta: "old3", Type: MessagesRoleUser}
	for _, m := range []*Messages{old1, old2, old3} {
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("创建旧消息失败: %v", err)
		}
	}

	if err := RewindTo(db, 1, old2.ID); err != nil {
		t.Fatalf("RewindTo 失败: %v", err)
	}
	// 补链：old1 为根、old2 挂 old1、old3 挂 old2
	rewindTestAssertParent(t, db, old1.ID, nil)
	rewindTestAssertParent(t, db, old2.ID, &old1.ID)
	rewindTestAssertParent(t, db, old3.ID, &old2.ID)
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != old2.ID {
		t.Fatalf("叶子应为 %d，得到 %v", old2.ID, leaf)
	}
	// 活跃分支 = old1 → old2，早于目标的历史没有消失
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{old1.ID, old2.ID})
	if total := rewindTestCount(t, db, 1); total != 3 {
		t.Fatalf("rewind 不应删除消息，总数应为 3，得到 %d", total)
	}
}

// TestRewindValidation 验证参数与目标校验：会话不存在、消息不存在、消息跨会话
// 等情况均报错，且失败不产生任何状态改动。
func TestRewindValidation(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	ms := rewindTestChain(t, db, 1, 2)
	branchTestChat(t, db, 2)
	other := &Messages{ChatID: 2, Delta: "other", Type: MessagesRoleUser}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("创建会话 2 的消息失败: %v", err)
	}

	cases := []struct {
		name   string
		db     *gorm.DB
		chatID uint32
		msgID  uint64
	}{
		{"db 为 nil", nil, 1, ms[0].ID},
		{"chatID 为 0", db, 0, ms[0].ID},
		{"msgID 为 0", db, 1, 0},
		{"会话不存在", db, 999, ms[0].ID},
		{"目标消息不存在", db, 1, 999999},
		{"目标消息属于其它会话", db, 1, other.ID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := RewindTo(tc.db, tc.chatID, tc.msgID); err == nil {
				t.Fatal("应返回错误")
			}
		})
	}

	// 失败不改动任何状态：叶子仍为 m2，活跃分支不变
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != ms[1].ID {
		t.Fatalf("叶子应保持 %d，得到 %v", ms[1].ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID, ms[1].ID})
}

// TestRewindFailureLeavesStateUntouched 验证校验失败发生在任何写入之前：
// 异常状态下失败的 rewind 不会触发补链修复。
func TestRewindFailureLeavesStateUntouched(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)

	old1 := &Messages{ChatID: 1, Delta: "old1", Type: MessagesRoleUser}
	old2 := &Messages{ChatID: 1, Delta: "old2", Type: MessagesRoleAgent}
	for _, m := range []*Messages{old1, old2} {
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("创建旧消息失败: %v", err)
		}
	}

	if err := RewindTo(db, 1, 999999); err == nil {
		t.Fatal("目标消息不存在应报错")
	}
	if leaf := branchTestLeaf(t, db, 1); leaf != nil {
		t.Fatalf("失败后叶子应保持 NULL，得到 %v", *leaf)
	}
	rewindTestAssertParent(t, db, old1.ID, nil)
	rewindTestAssertParent(t, db, old2.ID, nil)
}

// TestRewindOnlyTouchesTargetChat 验证 rewind 只影响目标会话。
func TestRewindOnlyTouchesTargetChat(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	branchTestChat(t, db, 2)
	ms1 := rewindTestChain(t, db, 1, 3)
	ms2 := rewindTestChain(t, db, 2, 3)

	if err := RewindTo(db, 1, ms1[0].ID); err != nil {
		t.Fatalf("RewindTo 失败: %v", err)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms1[0].ID})
	// 会话 2 不受影响
	if leaf := branchTestLeaf(t, db, 2); leaf == nil || *leaf != ms2[2].ID {
		t.Fatalf("会话 2 的叶子不应变化，期望 %d，得到 %v", ms2[2].ID, leaf)
	}
	branchTestAssertIDs(t, branchTestIDs(t, db, 2), []uint64{ms2[0].ID, ms2[1].ID, ms2[2].ID})
}

// TestRewindThenDeleteHead 验证 rewind 后再删除当前叶子：叶子回退到新分支的父节点，
// 子节点按 DeleteMessage 规则上提。
func TestRewindThenDeleteHead(t *testing.T) {
	db := setupBranchTest(t)
	branchTestChat(t, db, 1)
	ms := rewindTestChain(t, db, 1, 3)

	if err := RewindTo(db, 1, ms[1].ID); err != nil {
		t.Fatalf("RewindTo 失败: %v", err)
	}
	if err := DeleteMessage(db, ms[1].ID); err != nil {
		t.Fatalf("删除新的叶子失败: %v", err)
	}
	if leaf := branchTestLeaf(t, db, 1); leaf == nil || *leaf != ms[0].ID {
		t.Fatalf("叶子应回退到 %d，得到 %v", ms[0].ID, leaf)
	}
	// m3 上提到 m1
	rewindTestAssertParent(t, db, ms[2].ID, &ms[0].ID)
	branchTestAssertIDs(t, branchTestIDs(t, db, 1), []uint64{ms[0].ID})
}
