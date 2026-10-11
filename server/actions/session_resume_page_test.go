package actions

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// replayChunk 一次 session/resume（replayFrom=start）的回放结果。
type replayChunk struct {
	messages  []string // 按回放顺序收集的 messageId（user_message / agent_message）
	toolCalls []SessionUpdateUpdate
	cursor    string // 响应携带的下一页游标（alk.cxykevin.top/session/cursor）
}

// resumeChunk 执行一次带回放的分页请求，收集整消息通知、工具调用与下一页游标。
func (f *replayArgsFixture) resumeChunk(t *testing.T, cursor string) replayChunk {
	t.Helper()
	chunk := replayChunk{}
	call := func(method string, params any, _ *string) error {
		if method != "session/update" {
			return nil
		}
		update, ok := params.(SessionUpdate)
		if !ok {
			return nil
		}
		val, ok := update.Update.(SessionUpdateUpdate)
		if !ok {
			return nil
		}
		switch val.SessionUpdate {
		case "user_message", "agent_message":
			chunk.messages = append(chunk.messages, val.MessageID)
		case "tool_call_update":
			chunk.toolCalls = append(chunk.toolCalls, val)
		}
		return nil
	}
	resp, err := SessionResume(SessionResumeRequest{
		Cwd:        f.cwd,
		SessionID:  f.sessionID,
		ReplayFrom: &ReplayFrom{Type: "start"},
		Cursor:     cursor,
	}, call, 1)
	if err != nil {
		t.Fatalf("SessionResume replay failed: %v", err)
	}
	chunk.cursor = resp.Cursor
	return chunk
}

// insertHistory 顺序落库 n 条用户消息（历史回放的最小单位）。
func (f *replayArgsFixture) insertHistory(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		msg := structs.Messages{ChatID: f.chatID, Type: structs.MessagesRoleUser, Delta: fmt.Sprintf("history %d", i)}
		if err := f.db.Create(&msg).Error; err != nil {
			t.Fatalf("insert history message failed: %v", err)
		}
	}
}

// TestSessionResumeReplayPaginatesHistory 历史回放从**最新**（活跃分支末端）往更早方向分页：
// 首页是最后 sessionReplayPageSize 条消息并返回游标，原样回传游标即可继续取更早的一页，
// 回到最早一条后不再返回游标。
func TestSessionResumeReplayPaginatesHistory(t *testing.T) {
	f := newReplayArgsFixture(t)

	const extra = 5
	f.insertHistory(t, sessionReplayPageSize+extra)

	first := f.resumeChunk(t, "")
	if len(first.messages) != sessionReplayPageSize {
		t.Fatalf("首页应回放 %d 条消息，实际 %d", sessionReplayPageSize, len(first.messages))
	}
	if first.cursor == "" {
		t.Fatal("更早的消息尚未回放，响应应携带 alk.cxykevin.top/session/cursor")
	}
	// 首屏即最新一页：最早的 extra 条留在下一页
	if want := msgID(uint64(extra + 1)); first.messages[0] != want {
		t.Errorf("首页最早一条应为 %s（最新 %d 条），实际 %s", want, sessionReplayPageSize, first.messages[0])
	}
	if want := msgID(uint64(sessionReplayPageSize + extra)); first.messages[len(first.messages)-1] != want {
		t.Errorf("首页最后一条应为会话头 %s，实际 %s", want, first.messages[len(first.messages)-1])
	}

	second := f.resumeChunk(t, first.cursor)
	if len(second.messages) != extra {
		t.Fatalf("第二页应回放剩余 %d 条消息，实际 %d", extra, len(second.messages))
	}
	if second.cursor != "" {
		t.Errorf("已回放到最早一条，不应再返回游标，实际 %q", second.cursor)
	}

	// 两页合起来恰好是完整历史：无重复、无遗漏，按时间序（id 升序）拼回
	all := append(append([]string{}, second.messages...), first.messages...)
	if len(all) != sessionReplayPageSize+extra {
		t.Fatalf("两页消息数应合计 %d，实际 %d", sessionReplayPageSize+extra, len(all))
	}
	for i, id := range all {
		if want := msgID(uint64(i + 1)); id != want {
			t.Fatalf("第 %d 条回放消息应为 %s，实际 %s", i+1, want, id)
		}
	}
}

// TestSessionResumeReplayPageBoundaryKeepsToolCalls 页边界落在"agent 消息 → 它的工具结果"
// 之间时：工具调用在工具结果所在的那一页回放，既不重复回放 agent 消息，也不丢工具调用，
// toolCallId 中的 messageId 仍指向该 agent 消息。
func TestSessionResumeReplayPageBoundaryKeepsToolCalls(t *testing.T) {
	f := newReplayArgsFixture(t)

	// agent 消息 + 它的工具结果排在最前，再用 user 消息把总数填到 sessionReplayPageSize+1 条：
	// 工具结果正好是首页（最新一页）最早一条，它的 agent 消息落在下一页，
	// 首页的工具渲染上下文只能由页外回扫补齐。
	agentID := f.insertAgentToolCall(t, []map[string]any{{
		"name":       "run",
		"id":         "tool_1",
		"parameters": map[string]any{"type": "shell", "reason": "t", "command": "echo hi"},
	}})
	f.insertHistory(t, sessionReplayPageSize-1)

	first := f.resumeChunk(t, "")
	if len(first.messages) != sessionReplayPageSize-1 {
		t.Fatalf("首页应回放 %d 条 user 消息（工具结果不计入整消息），实际 %d", sessionReplayPageSize-1, len(first.messages))
	}
	if len(first.toolCalls) != 1 {
		t.Fatalf("首页最早一条是工具结果，应回放其 agent 消息的工具调用，实际 %d 条", len(first.toolCalls))
	}
	call := first.toolCalls[0]
	if want := fmt.Sprintf("call_%d_%d_%s", f.chatID, agentID, "tool_1"); call.ToolCallID != want {
		t.Errorf("工具调用 ID 应为 %s，实际 %s", want, call.ToolCallID)
	}
	if call.Kind != "execute" {
		t.Errorf("run 工具调用的 kind 应为 execute，实际 %q", call.Kind)
	}
	if first.cursor == "" {
		t.Fatal("agent 消息本身尚未回放，响应应携带游标")
	}

	second := f.resumeChunk(t, first.cursor)
	if len(second.messages) != 1 {
		t.Errorf("第二页只有那条 agent 消息，实际 %d 条", len(second.messages))
	}
	if len(second.toolCalls) != 0 {
		t.Errorf("工具结果已在首页回放，第二页不应重复回放工具调用，实际 %d 条", len(second.toolCalls))
	}
	if second.cursor != "" {
		t.Errorf("已回放到最早一条，不应再返回游标，实际 %q", second.cursor)
	}
}

// TestSessionResumeReplayCursorValidation 非法游标（非本服务端生成）返回 invalid cursor；
// 陈旧游标（指向更靠后的位置、其消息已被删除或 rewind 出活跃分支）仍按 id 稳定地取更早的
// 那一段，不重复不遗漏；已回到最早一条时返回空页且不返回游标；未请求回放时不返回游标。
func TestSessionResumeReplayCursorValidation(t *testing.T) {
	f := newReplayArgsFixture(t)
	f.insertHistory(t, 3)

	noop := func(string, any, *string) error { return nil }
	if _, err := SessionResume(SessionResumeRequest{
		Cwd:        f.cwd,
		SessionID:  f.sessionID,
		ReplayFrom: &ReplayFrom{Type: "start"},
		Cursor:     "not-a-cursor",
	}, noop, 1); err == nil || !strings.Contains(err.Error(), "invalid cursor") {
		t.Fatalf("非法游标应返回 invalid cursor 错误，实际 %v", err)
	}

	// 陈旧游标（指向远超当前历史的 id，例如游标消息已被删除）：严格早于该 id 的消息全在
	// → 返回整段历史且不返回游标（不重复、不遗漏、不报错）
	stale, err := encodeReplayCursor(&structs.Messages{ID: 1 << 20})
	if err != nil {
		t.Fatalf("encodeReplayCursor failed: %v", err)
	}
	chunk := f.resumeChunk(t, stale)
	if len(chunk.messages) != 3 || chunk.cursor != "" {
		t.Fatalf("陈旧游标应回放全部 3 条更早消息且不返回游标，实际 %d 条消息 / cursor=%q", len(chunk.messages), chunk.cursor)
	}

	// 已回到最早一条（游标指向第一条消息之前）：空页且无游标
	oldest, err := encodeReplayCursor(&structs.Messages{ID: 1})
	if err != nil {
		t.Fatalf("encodeReplayCursor failed: %v", err)
	}
	if empty := f.resumeChunk(t, oldest); len(empty.messages) != 0 || empty.cursor != "" {
		t.Fatalf("游标已在最早一条之前，应回放空页且不返回游标，实际 %d 条消息 / cursor=%q", len(empty.messages), empty.cursor)
	}

	resp, err := SessionResume(SessionResumeRequest{Cwd: f.cwd, SessionID: f.sessionID}, noop, 1)
	if err != nil {
		t.Fatalf("SessionResume without replay failed: %v", err)
	}
	if resp.Cursor != "" {
		t.Errorf("未请求回放时不应返回游标，实际 %q", resp.Cursor)
	}
}
