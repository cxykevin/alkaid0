package trace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestRenderTraceBlocks_VirtualContentDiffTail 虚拟对象（@tree）内容变化但没有新事件时走方案2：
// 旧块字节稳定留在原锚点、增量块追加到列表末尾；基线/锚点都不推进（下一次 diff 旧端仍稳定）。
// 用大内容确保 diff 比原文件短，否则会触发硬条件退化为整块。
func TestRenderTraceBlocks_VirtualContentDiffTail(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	const vpath = "@test-virtual-diff"
	var old strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&old, "node-%03d some tree entry text\n", i)
	}
	content := old.String()
	delete(virtualContentProviders, vpath)
	RegisterVirtualContent(vpath, func(session *structs.Chats) (string, bool) { return content, true })
	defer delete(virtualContentProviders, vpath)

	if err := db.Create(&structs.Traces{ChatID: 1, Path: vpath, AgentID: "test_agent", TraceID: 5,
		LastContent: old.String(), AnchorMsgID: 33}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}
	session := &structs.Chats{
		ID: 1, DB: db, NowAgent: "test_agent", Root: t.TempDir(),
		TemporyDataOfSession: map[string]any{structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{}},
	}
	content = old.String() + "node-200 appended after the baseline\n"

	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}
	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	p := plans[vpath]
	if p == nil || p.Mode != AnchorDiffTail || p.PrevMsgID != 33 {
		t.Fatalf("无事件承载的内容变化应走「旧块+末尾 diff」: %+v", p)
	}
	diffPlans, _ := session.TemporyDataOfSession[structs.TempKeyTraceDiffPlan].(map[string]DiffPlan)
	if !diffPlans[vpath].Keep {
		t.Fatal("应产出方案2 候选（Keep=true）")
	}
	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = ?", vpath).First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != 33 || tr.LastContent != old.String() {
		t.Errorf("方案2 不得推进锚点/基线: anchor=%d lastLen=%d", tr.AnchorMsgID, len(tr.LastContent))
	}
}

// TestSetTraceAnchor_UpsertExistingRow 已存在的行（含 anchor_msg_id=0 的历史行）也必须被 upsert 更新，
// 且不得清掉其它列（last_content）。虚拟对象（@tree）在库里通常已有行，走的是冲突更新分支。
func TestSetTraceAnchor_UpsertExistingRow(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	if err := db.Create(&structs.Traces{ChatID: 1, Path: "@tree", AgentID: "test_agent", TraceID: 3, LastContent: "old"}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}
	session := &structs.Chats{ID: 1, DB: db, NowAgent: "test_agent"}
	SetTraceAnchor(session, "@tree", 4321)

	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = '@tree'").First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != 4321 {
		t.Errorf("upsert 必须更新已存在行的锚点: want 4321 got %d", tr.AnchorMsgID)
	}
	if tr.LastContent != "old" {
		t.Errorf("upsert 不得清掉其它列: got %q", tr.LastContent)
	}
}

// TestRenderTraceBlocks_VirtualContentTail 虚拟对象（@tree 等）纳入同一套落位/差分机制：
//   - 首次注入没有事件可锚 → 末尾落位（Tail）；
//   - 内容未变化且已有锚点 → 留在原位（Full@锚点）；
//   - 内容变化但无新事件（被别的工具改动）→ 方案2：旧块留原锚点 + 增量块追加末尾；
//   - diff 超过 2× 安全阀（大重写，或内容太短导致 diff 固定开销占比过高）→ 整块末尾落位；
//   - 有晚于锚点的 edit 事件 → 完整块锚到事件位置；
//   - 内容已自带行号，不得被二次编号。
func TestRenderTraceBlocks_VirtualContentTail(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	// 用几十行的内容：统一 diff 有固定开销（文件头 + @@ + 上下文行），内容太短时
	// 一次小改也会超过比例上限，那是比例阈值的固有边界（见 3c 用例）。
	const vpath = "@test-virtual"
	var seed strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&seed, "node-%02d some tree entry text\n", i)
	}
	content := seed.String()
	delete(virtualContentProviders, vpath)
	RegisterVirtualContent(vpath, func(session *structs.Chats) (string, bool) {
		return content, true
	})
	defer delete(virtualContentProviders, vpath)

	session := &structs.Chats{
		ID:       1,
		DB:       db,
		NowAgent: "test_agent",
		Root:     t.TempDir(),
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{},
		},
	}
	plansOf := func() map[string]*AnchorPlan {
		plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
		return plans
	}

	// 1) 首次注入：无事件、无锚点 → Tail，且内容不二次编号
	_, blocks, err := RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}
	fb, ok := blocks[vpath]
	if !ok {
		t.Fatalf("虚拟对象必须进 eventBlocks")
	}
	if fb.Text != content {
		t.Errorf("虚拟对象内容不得二次加行号: want %q got %q", content, fb.Text)
	}
	if p := plansOf()[vpath]; p == nil || p.Mode != AnchorTail {
		t.Fatalf("首次注入（无事件）必须末尾落位: %+v", plansOf()[vpath])
	}
	// 模拟 build 层末尾插入完成后的回写
	SetTraceAnchor(session, vpath, 77)
	AdvanceTraceLastContent(session, vpath, content)

	// 2) 内容未变化：留在上次注入锚点
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks #2: %v", err)
	}
	if p := plansOf()[vpath]; p == nil || p.Mode != AnchorFull || p.MsgID != 77 {
		t.Fatalf("内容未变化时必须留在原锚点: %+v", plansOf()[vpath])
	}

	// 3) 内容变化且没有新事件（例如 edit/run 改了工作区）：小增量 → 方案2（旧块留原锚点 +
	//    增量块追加末尾），不再整块前移
	content = seed.String() + "node-40 appended line\n"
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks #3: %v", err)
	}
	if p := plansOf()[vpath]; p == nil || p.Mode != AnchorDiffTail || p.PrevMsgID != 77 {
		t.Fatalf("无新事件承载的小增量应走「旧块+末尾 diff」: %+v", plansOf()[vpath])
	}
	// 3b) 大幅重写：diff 超过 2× 安全阀 → 退化为整块末尾落位
	var rewritten strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&rewritten, "rewritten-%02d completely different payload\n", i)
	}
	content = rewritten.String()
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks #3b: %v", err)
	}
	if p := plansOf()[vpath]; p == nil || p.Mode != AnchorTail {
		t.Fatalf("diff 超过上限时必须退化为整块末尾落位: %+v", plansOf()[vpath])
	}
	// 还原基线继续后续用例
	AdvanceTraceLastContent(session, vpath, content)

	// 4) 模型 edit 了 @tree（事件晚于锚点）→ 完整块锚到事件位置，并 upsert 出数据库行
	session.TemporyDataOfSession[structs.TempKeyTraceEvents] = map[string]*structs.TraceEvent{
		vpath: {MsgID: 200, ToolCallID: "call_v"},
	}
	content = "tree line 1\n"
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks #4: %v", err)
	}
	p := plansOf()[vpath]
	if p == nil || p.Mode != AnchorFull || p.MsgID != 200 {
		t.Fatalf("有事件时必须锚到事件位置: %+v", p)
	}
	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = ?", vpath).First(&tr).Error; err != nil {
		t.Fatalf("虚拟对象必须能自动建 trace 行（锚点/基线持久化）: %v", err)
	}
	if tr.AnchorMsgID != 200 {
		t.Errorf("锚点必须写入数据库: want 200 got %d", tr.AnchorMsgID)
	}
	if tr.LastContent != content {
		t.Errorf("旧端存档必须写入数据库: want %q got %q", content, tr.LastContent)
	}
}
