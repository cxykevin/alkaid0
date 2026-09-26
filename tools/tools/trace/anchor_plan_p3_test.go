package trace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestRenderTraceBlocks_AnchorBackfill 历史库行（AnchorMsgID=0）且内容未变化时：
// 按最新事件落位并回填注入锚点，位置与旧行为一致；LastContent 已等于当前内容，不再重复推进。
// 见 docs/trace-cache-spec.md §4.2 与 §5.2。
func TestRenderTraceBlocks_AnchorBackfill(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	tmpDir := t.TempDir()
	content := "line A1\nline A2\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte(content), 0644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := db.Create(&structs.Traces{ChatID: 1, Path: "a.txt", TraceID: 1, AgentID: "test_agent", LastContent: content}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}

	session := &structs.Chats{
		ID:       1,
		DB:       db,
		NowAgent: "test_agent",
		Root:     tmpDir,
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
				"a.txt": {MsgID: 42, ToolCallID: "call_1", IsEdit: false},
			},
		},
	}
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}

	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = 'a.txt'").First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != 42 {
		t.Errorf("历史行必须回填注入锚点: want 42 got %d", tr.AnchorMsgID)
	}
	if tr.LastContent != content {
		t.Errorf("内容未变化时不应改写旧端存档: want %q got %q", content, tr.LastContent)
	}
	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	p := plans["a.txt"]
	if p == nil || p.Mode != AnchorFull || p.MsgID != 42 {
		t.Errorf("未变化的历史行应按最新事件完整块落位: %+v", p)
	}
}

// TestRenderTraceBlocks_TempPlanTail 内容变化但没有新事件承载（最新事件早于上次注入锚点）时，
// 落位必须是"末尾前移"而不是原地更新：块的前移由 build 层执行，这里只验证计划与锚点不漂移。
func TestRenderTraceBlocks_TempPlanTail(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	// @temp 内容来自 ReferFiles；事件早于上次注入锚点（E=10 <= A=20）→ Tail
	if err := db.Create(&structs.ReferFiles{ChatID: 1, Path: "run/1", Content: "fresh-output"}).Error; err != nil {
		t.Fatalf("create refer: %v", err)
	}
	if err := db.Create(&structs.Traces{ChatID: 1, Path: "@temp/run/1", TraceID: 1, AgentID: "test_agent",
		LastContent: "stale-output", AnchorMsgID: 20}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}

	session := &structs.Chats{
		ID:       1,
		DB:       db,
		NowAgent: "test_agent",
		Root:     t.TempDir(),
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
				"@temp/run/1": {MsgID: 10, ToolCallID: "call_1"},
			},
		},
	}
	if _, _, err := RenderTraceBlocks(session); err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}
	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	p := plans["@temp/run/1"]
	if p == nil || p.Mode != AnchorTail || !p.Full {
		t.Fatalf("无新事件承载的内容变化必须走末尾前移: %+v", p)
	}
	// 末尾落位由 build 层实际插入后回写锚点/基线，这里不应提前改写
	var tr structs.Traces
	if err := db.Where("chat_id = 1 AND path = '@temp/run/1'").First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != 20 {
		t.Errorf("计划阶段不得提前改写锚点: want 20 got %d", tr.AnchorMsgID)
	}
}
