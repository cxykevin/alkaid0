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

// TestSessionResumeReplayPaginatesHistory 历史回放按 sessionReplayPageSize 分页：
// 首页从会话头开始并返回游标，原样回传游标即可继续下一页，回放完毕后不再返回游标。
func TestSessionResumeReplayPaginatesHistory(t *testing.T) {
	f := newReplayArgsFixture(t)

	const extra = 5
	f.insertHistory(t, sessionReplayPageSize+extra)

	first := f.resumeChunk(t, "")
	if len(first.messages) != sessionReplayPageSize {
		t.Fatalf("首页应回放 %d 条消息，实际 %d", sessionReplayPageSize, len(first.messages))
	}
	if first.cursor == "" {
		t.Fatal("首页之后仍有未回放消息，响应应携带 alk.cxykevin.top/session/cursor")
	}
	if first.messages[0] != msgID(1) {
		t.Errorf("首页应从会话头开始（第一条为 %s），实际 %s", msgID(1), first.messages[0])
	}

	second := f.resumeChunk(t, first.cursor)
	if len(second.messages) != extra {
		t.Fatalf("第二页应回放剩余 %d 条消息，实际 %d", extra, len(second.messages))
	}
	if second.cursor != "" {
		t.Errorf("历史已回放完毕，不应再返回游标，实际 %q", second.cursor)
	}

	// 两页合起来恰好是完整历史：无重复、无遗漏、顺序与 id 升序一致
	all := append(append([]string{}, first.messages...), second.messages...)
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

	// 前 sessionReplayPageSize-1 条填满首页，紧随其后的 agent 消息正好是第
	// sessionReplayPageSize 条，它的工具结果因此落到第二页开头。
	f.insertHistory(t, sessionReplayPageSize-1)
	agentID := f.insertAgentToolCall(t, []map[string]any{{
		"name":       "run",
		"id":         "tool_1",
		"parameters": map[string]any{"type": "shell", "reason": "t", "command": "echo hi"},
	}})

	first := f.resumeChunk(t, "")
	if len(first.messages) != sessionReplayPageSize {
		t.Fatalf("首页应回放 %d 条消息，实际 %d", sessionReplayPageSize, len(first.messages))
	}
	if len(first.toolCalls) != 0 {
		t.Fatalf("首页不含工具结果，不应回放工具调用，实际 %d 条", len(first.toolCalls))
	}
	if first.cursor == "" {
		t.Fatal("首页之后仍有未回放的工具结果，响应应携带游标")
	}

	second := f.resumeChunk(t, first.cursor)
	if len(second.messages) != 0 {
		t.Errorf("第二页只有工具结果，不应重复回放整消息，实际 %d 条", len(second.messages))
	}
	if len(second.toolCalls) != 1 {
		t.Fatalf("第二页应回放上一页 agent 消息的工具调用，实际 %d 条", len(second.toolCalls))
	}
	call := second.toolCalls[0]
	if want := fmt.Sprintf("call_%d_%d_%s", f.chatID, agentID, "tool_1"); call.ToolCallID != want {
		t.Errorf("工具调用 ID 应为 %s，实际 %s", want, call.ToolCallID)
	}
	if call.Kind != "execute" {
		t.Errorf("run 工具调用的 kind 应为 execute，实际 %q", call.Kind)
	}
	if second.cursor != "" {
		t.Errorf("历史已回放完毕，不应再返回游标，实际 %q", second.cursor)
	}
}

// TestSessionResumeReplayCursorValidation 非法游标（非本服务端生成）返回 invalid cursor；
// 游标指向的消息已不存在（被删除或 rewind 出活跃分支）时从其位置之后继续，不回退不报错；
// 未请求回放时不返回游标。
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

	// 陈旧游标（指向远超当前历史的 id）：跳过已消费位置后继续，此处已到末尾 → 空页且无游标
	stale, err := encodeReplayCursor(&structs.Messages{ID: 1 << 20})
	if err != nil {
		t.Fatalf("encodeReplayCursor failed: %v", err)
	}
	chunk := f.resumeChunk(t, stale)
	if len(chunk.messages) != 0 || chunk.cursor != "" {
		t.Fatalf("陈旧游标应回放空页且不返回游标，实际 %d 条消息 / cursor=%q", len(chunk.messages), chunk.cursor)
	}

	resp, err := SessionResume(SessionResumeRequest{Cwd: f.cwd, SessionID: f.sessionID}, noop, 1)
	if err != nil {
		t.Fatalf("SessionResume without replay failed: %v", err)
	}
	if resp.Cursor != "" {
		t.Errorf("未请求回放时不应返回游标，实际 %q", resp.Cursor)
	}
}
