package structs

import (
	"fmt"
	"sync"
	"testing"
)

// TestToolCallingConcurrent 验证 SetToolCalling/SnapshotToolCalling/HasToolCalling 并发安全。
// 需在 -race 下运行：多 goroutine 交替写、读、清空，任何 map 并发读写都会使测试崩溃。
func TestToolCallingConcurrent(t *testing.T) {
	c := &Chats{}
	var wg sync.WaitGroup
	const workers = 8
	const perWorker = 200

	// 并发写（模拟流式阶段 OnHook 每 token 写入）
	for i := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := range perWorker {
				c.SetToolCalling(fmt.Sprintf("call_%d_%d", w, j), fmt.Sprintf("v-%d-%d", w, j), "test")
			}
		}(i)
	}
	// 并发读+清空（模拟 SetCallback goroutine 快照广播）
	for range workers {
		wg.Go(func() {
			for range perWorker {
				_, _, _ = c.SnapshotToolCalling()
				_ = c.HasToolCalling()
			}
		})
	}
	wg.Wait()
	// 并发写读清空下不 panic（-race 无竞态报告）即通过；
	// 不断言最终为空——写 goroutine 可能最后写入。
}

// TestToolCallingSnapshotValue 验证 SnapshotToolCalling 快照/清空语义与 SetLatest/ResetLatest。
func TestToolCallingSnapshotValue(t *testing.T) {
	c := &Chats{}
	c.SetToolCalling("call_1", map[string]any{"a": 1}, "edit")

	if !c.HasToolCalling() {
		t.Fatal("SetToolCalling 后 HasToolCalling 应为 true")
	}

	ctx, typ, streaming := c.SnapshotToolCalling()
	if len(ctx) != 1 || typ["call_1"] != "edit" {
		t.Errorf("快照内容错误: ctx=%v typ=%v", ctx, typ)
	}
	// 默认（State=Idle）写入应为最终状态（非流式）
	if streaming["call_1"] {
		t.Error("State=Idle 下 SetToolCalling 应标记为最终状态（streaming=false）")
	}
	if c.HasToolCalling() {
		t.Error("SnapshotToolCalling 应清空 ToolCallingContext")
	}

	// SetLatest 覆盖 Latest
	c.SetLatest(ctx, typ)
	latestCtx, latestTyp := c.SnapshotLatest()
	if len(latestCtx) != 1 || latestTyp["call_1"] != "edit" {
		t.Errorf("Latest 快照错误: %v %v", latestCtx, latestTyp)
	}

	// ResetLatest 清空 Latest
	c.ResetLatest()
	latestCtx2, _ := c.SnapshotLatest()
	if len(latestCtx2) != 0 {
		t.Error("ResetLatest 应清空 Latest")
	}

	// ClearToolCalling 清空当前
	c.SetToolCalling("call_2", "x", "run")
	c.ClearToolCalling()
	if c.HasToolCalling() {
		t.Error("ClearToolCalling 应清空当前上下文")
	}
}

// TestToolCallingTerminalID 验证工具调用携带的 run id / terminal id 会被最终快照带出并清空。
func TestToolCallingTerminalID(t *testing.T) {
	c := &Chats{}
	c.SetToolCalling("call_1", map[string]any{"name": "run"}, "run")
	c.SetToolCallingRunID("call_1", "@temp/run/7")
	c.SetToolCallingTerminalID("call_1", "@temp/run/7")

	ctx, typ, runIDs, terminalIDs := c.TakeFinalToolCallingWithIDs()
	if len(ctx) != 1 || typ["call_1"] != "run" {
		t.Errorf("快照内容错误: ctx=%v typ=%v", ctx, typ)
	}
	if runIDs["call_1"] != "@temp/run/7" {
		t.Errorf("run_id = %q, want @temp/run/7", runIDs["call_1"])
	}
	if terminalIDs["call_1"] != "@temp/run/7" {
		t.Errorf("terminal_id = %q, want @temp/run/7", terminalIDs["call_1"])
	}

	// 取出后清空，不残留到下一轮
	if len(c.ToolCallingRunID) != 0 || len(c.ToolCallingTerminalID) != 0 {
		t.Errorf("快照后应清空 run/terminal id: %v %v", c.ToolCallingRunID, c.ToolCallingTerminalID)
	}

	// 空参数安全
	c.SetToolCallingTerminalID("", "@temp/run/1")
	c.SetToolCallingTerminalID("call_x", "")
	if len(c.ToolCallingTerminalID) != 0 {
		t.Errorf("空 id/terminal 不应写入: %v", c.ToolCallingTerminalID)
	}
	var nilChats *Chats
	nilChats.SetToolCallingTerminalID("call_1", "@temp/run/1")
}

// TestToolCallingRawParams 验证原始参数（用于把展示 content 规范化成完整参数渲染）的
// 存取与清空语义，以及工具调用 ID 解析（call_<chatID>_<msgID>_<toolID>）。
func TestToolCallingRawParams(t *testing.T) {
	c := &Chats{}
	c.SetToolCallingRawParams("call_9_42_tool_a", map[string]any{"command": "echo hi"})
	if raw, ok := c.TakeToolCallingRawParams("call_9_42_tool_a").(map[string]any); !ok || raw["command"] != "echo hi" {
		t.Errorf("TakeToolCallingRawParams = %v, want command=echo hi", c.TakeToolCallingRawParams("call_9_42_tool_a"))
	}
	// 取出后即清空，重复取返回 nil
	if raw := c.TakeToolCallingRawParams("call_9_42_tool_a"); raw != nil {
		t.Errorf("原始参数取出后应清空，实际 %v", raw)
	}
	// 空 id / nil 值不写入
	c.SetToolCallingRawParams("", map[string]any{"a": 1})
	c.SetToolCallingRawParams("call_1", nil)
	if len(c.ToolCallingRawParams) != 0 {
		t.Errorf("空 id/nil 值不应写入: %v", c.ToolCallingRawParams)
	}

	// ClearToolCalling 一并清理未消费的原始参数
	c.SetToolCallingRawParams("call_1", map[string]any{"a": 1})
	c.ClearToolCalling()
	if len(c.ToolCallingRawParams) != 0 {
		t.Errorf("ClearToolCalling 应清空原始参数: %v", c.ToolCallingRawParams)
	}

	// 工具调用 ID 解析
	if msgID, toolID := splitToolCallingID("call_9_42_tool_a"); msgID != 42 || toolID != "tool_a" {
		t.Errorf("splitToolCallingID = (%d, %q), want (42, tool_a)", msgID, toolID)
	}
	for _, bad := range []string{"", "call_9", "tool_9_42_a", "call_9_x_a"} {
		if msgID, toolID := splitToolCallingID(bad); msgID != 0 || toolID != "" {
			t.Errorf("splitToolCallingID(%q) = (%d, %q), want (0, \"\")", bad, msgID, toolID)
		}
	}
}

// TestSetToolCallingNilReceiver 空指针调用封装方法不应 panic。
func TestSetToolCallingNilReceiver(t *testing.T) {
	var c *Chats
	c.SetToolCalling("id", "v", "t") // 应安全返回
	if c.HasToolCalling() {
		t.Error("nil receiver HasToolCalling 应为 false")
	}
}
