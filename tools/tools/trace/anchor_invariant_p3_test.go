package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// invariantTestContent 生成 lines 行等长内容；changeLine >= 0 时改写该行（其余行保持一致，
// 使一次小改动的 unified diff 占比足够低，方案2 的硬条件成立）。
func invariantTestContent(lines, changeLine int) string {
	var b strings.Builder
	for i := range lines {
		if i == changeLine {
			fmt.Fprintf(&b, "trace line %02d CHANGED payload text\n", i)
			continue
		}
		fmt.Fprintf(&b, "trace line %02d original payload text\n", i)
	}
	return b.String()
}

// TestRenderTraceBlocks_StalePrevEventKeepsDiff 「窗口内最早事件」早于上次注入锚点时
// （典型：read unread=true 删过行、或块被前移过之后又重新 read：AnchorMsgID=20 是重新 read
// 的注入位置，prevEvents=5 是被 unread 掉的那次旧 read），旧块的字节仍然在 AnchorMsgID 上，
// 因此必须走方案2：旧块留在 20、增量块锚最新事件 30。
//
// 早期实现把「最早事件」当旧端锚点并要求它等于 AnchorMsgID，这条路径会退化成整块搬家，
// 从旧锚点到列表末尾整段按未命中重算；而且搬家一次后该等式永久不成立（见下一个用例）。
func TestRenderTraceBlocks_StalePrevEventKeepsDiff(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	tmpDir := t.TempDir()
	oldContent := invariantTestContent(40, -1)
	newContent := invariantTestContent(40, 7)
	if err := os.WriteFile(filepath.Join(tmpDir, "stale.txt"), []byte(newContent), 0644); err != nil {
		t.Fatalf("write stale.txt: %v", err)
	}
	if err := db.Create(&structs.Traces{ChatID: 1, Path: "stale.txt", TraceID: 1, AgentID: "test_agent",
		LastContent: oldContent, AnchorMsgID: 20}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}

	session := &structs.Chats{
		ID:       1,
		DB:       db,
		NowAgent: "test_agent",
		Root:     tmpDir,
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
				"stale.txt": {MsgID: 30, ToolCallID: "call_edit"},
			},
			structs.TempKeyTracePrevEvents: map[string]*structs.TraceEvent{
				"stale.txt": {MsgID: 5, ToolCallID: "call_old_read"},
			},
		},
	}
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}

	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	p := plans["stale.txt"]
	if p == nil || p.Mode != AnchorDiff || p.MsgID != 30 || p.PrevMsgID != 20 {
		t.Fatalf("旧块应按注入锚点（20）保留，增量块锚最新事件（30）: %+v", p)
	}
	diffPlans, _ := session.TemporyDataOfSession[structs.TempKeyTraceDiffPlan].(map[string]DiffPlan)
	if !diffPlans["stale.txt"].Keep {
		t.Fatal("应产出方案2 候选（Keep=true）")
	}

	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = 'stale.txt'").First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != 20 || tr.LastContent != oldContent {
		t.Errorf("方案2 不得推进锚点/基线: anchor=%d lastLen=%d", tr.AnchorMsgID, len(tr.LastContent))
	}
}

// TestRenderTraceBlocks_NoRatchetAfterMove 搬家（完整块落到最新事件、AnchorMsgID 被推进）之后，
// 下一次内容变化仍必须能走方案2。早期实现用「窗口内最早事件」当旧端锚点并要求它等于
// AnchorMsgID：锚点被推进到 90、最早事件仍停在 5，等式永远不成立 → 此后每轮都整块搬家，
// 旧锚点之后（往往就是整个上下文）全部按未命中计费。
func TestRenderTraceBlocks_NoRatchetAfterMove(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	tmpDir := t.TempDir()
	oldContent := invariantTestContent(40, -1)
	newContent := invariantTestContent(40, 21)
	if err := os.WriteFile(filepath.Join(tmpDir, "ratchet.txt"), []byte(newContent), 0644); err != nil {
		t.Fatalf("write ratchet.txt: %v", err)
	}
	if err := db.Create(&structs.Traces{ChatID: 1, Path: "ratchet.txt", TraceID: 1, AgentID: "test_agent",
		LastContent: oldContent, AnchorMsgID: 90}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}

	session := &structs.Chats{
		ID:       1,
		DB:       db,
		NowAgent: "test_agent",
		Root:     tmpDir,
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
				"ratchet.txt": {MsgID: 100, ToolCallID: "call_edit"},
			},
			structs.TempKeyTracePrevEvents: map[string]*structs.TraceEvent{
				"ratchet.txt": {MsgID: 5, ToolCallID: "call_old_read"},
			},
		},
	}
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}

	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	p := plans["ratchet.txt"]
	if p == nil || p.Mode != AnchorDiff || p.MsgID != 100 || p.PrevMsgID != 90 {
		t.Fatalf("搬家一次之后仍必须按 AnchorMsgID 保留旧块: %+v", p)
	}

	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = 'ratchet.txt'").First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != 90 {
		t.Errorf("方案2 不得推进锚点: want 90 got %d", tr.AnchorMsgID)
	}
}

// TestRenderTraceBlocks_StableAnchorKeepsDiff 旧端位置与锚点一致（连续编辑的稳态）时仍走方案2：
// 旧块留在原位、增量块锚到最新事件，锚点与基线都不推进（下一轮旧端依旧稳定）。
func TestRenderTraceBlocks_StableAnchorKeepsDiff(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	tmpDir := t.TempDir()
	oldContent := invariantTestContent(40, -1)
	newContent := invariantTestContent(40, 11)
	if err := os.WriteFile(filepath.Join(tmpDir, "steady.txt"), []byte(newContent), 0644); err != nil {
		t.Fatalf("write steady.txt: %v", err)
	}
	if err := db.Create(&structs.Traces{ChatID: 1, Path: "steady.txt", TraceID: 1, AgentID: "test_agent",
		LastContent: oldContent, AnchorMsgID: 30}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}

	session := &structs.Chats{
		ID:       1,
		DB:       db,
		NowAgent: "test_agent",
		Root:     tmpDir,
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
				"steady.txt": {MsgID: 40, ToolCallID: "call_edit"},
			},
			structs.TempKeyTracePrevEvents: map[string]*structs.TraceEvent{
				"steady.txt": {MsgID: 30, ToolCallID: "call_read"},
			},
		},
	}
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}

	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	p := plans["steady.txt"]
	if p == nil || p.Mode != AnchorDiff || p.MsgID != 40 || p.PrevMsgID != 30 {
		t.Fatalf("旧端位置稳定时应走方案2（旧块原位 + 增量块锚事件）: %+v", p)
	}
	diffPlans, _ := session.TemporyDataOfSession[structs.TempKeyTraceDiffPlan].(map[string]DiffPlan)
	if !diffPlans["steady.txt"].Keep {
		t.Fatal("应产出方案2 候选（Keep=true）")
	}

	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = 'steady.txt'").First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != 30 || tr.LastContent != oldContent {
		t.Errorf("方案2 不得推进锚点/基线: anchor=%d lastLen=%d", tr.AnchorMsgID, len(tr.LastContent))
	}
}

// TestTrace_VirtualUnreadSuppressAndRestore 虚拟对象（@tree/@task/@memory）的 read unread=true
// 不能删 traces 行：内容由 provider 每轮重算、没有独立数据源，删行会让下一轮补合成行
// （基线/锚点归零）并按"首次注入"把整块内容搬到消息列表末尾，旧锚点之后的前缀缓存整段失效。
// 现在改为会话级抑制：保留行与基线，注入跳过；再次 read 即恢复（内容未变时留在原锚点）。
func TestTrace_VirtualUnreadSuppressAndRestore(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	const vpath = "@test-virtual-unread"
	content := "tree line 1\ntree line 2\n"
	delete(virtualContentProviders, vpath)
	RegisterVirtualContent(vpath, func(session *structs.Chats) (string, bool) { return content, true })
	defer delete(virtualContentProviders, vpath)

	if err := db.Create(&structs.Traces{ChatID: 1, Path: vpath, TraceID: 2, AgentID: "test_agent",
		LastContent: content, AnchorMsgID: 42}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}
	session := &structs.Chats{
		ID:                   1,
		DB:                   db,
		NowAgent:             "test_agent",
		Root:                 t.TempDir(),
		TemporyDataOfSession: map[string]any{},
	}
	call := func(unread bool) {
		pathVal := any(vpath)
		unreadVal := any(unread)
		_, _, result, err := Trace(session, map[string]*any{"path": &pathVal, "unread": &unreadVal}, nil)
		if err != nil {
			t.Fatalf("Trace(unread=%v): %v", unread, err)
		}
		successPtr, ok := result["success"]
		if !ok || successPtr == nil || !(*successPtr).(bool) {
			t.Fatalf("Trace(unread=%v) 必须成功: %+v", unread, result)
		}
		if errPtr, bad := result["error"]; bad && errPtr != nil {
			t.Fatalf("Trace(unread=%v) 不应报错: %+v", unread, result)
		}
	}

	call(true)

	// 行必须保留（删行会让基线/锚点归零）
	var traced int64
	if err := db.Model(&structs.Traces{}).Where("chat_id = 1 AND path = ?", vpath).Count(&traced).Error; err != nil {
		t.Fatalf("count trace: %v", err)
	}
	if traced != 1 {
		t.Fatalf("虚拟对象 unread 必须保留 traces 行（否则下一轮整块重注入）: got %d", traced)
	}
	_, blocks, err := RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks(suppressed): %v", err)
	}
	if _, ok := blocks[vpath]; ok {
		t.Error("被抑制的虚拟对象不得再注入内容块")
	}
	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	if plans[vpath] != nil {
		t.Errorf("被抑制的虚拟对象不得产出落位计划: %+v", plans[vpath])
	}

	// 再次 read → 恢复注入，且内容未变化时留在原锚点（前缀缓存不丢）
	call(false)
	_, blocks, err = RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks(restored): %v", err)
	}
	if _, ok := blocks[vpath]; !ok {
		t.Fatal("重新 read 后必须恢复注入内容块")
	}
	plans, _ = session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	if p := plans[vpath]; p == nil || p.Mode != AnchorFull || p.MsgID != 42 {
		t.Errorf("恢复注入时必须留在原锚点: %+v", plans[vpath])
	}
	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = ?", vpath).First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != 42 || tr.LastContent != content {
		t.Errorf("恢复注入不得改写锚点/基线: anchor=%d last=%q", tr.AnchorMsgID, tr.LastContent)
	}
}
