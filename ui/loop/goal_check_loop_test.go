package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	toolsindex "github.com/cxykevin/alkaid0/tools/index"
	u "github.com/cxykevin/alkaid0/utils"
)

// === goal-check loop 集成测试 ===
//
// 用 httptest 假 OpenAI 服务完整跑「主 agent → goal-check → 打回 → 主 agent → 校验通过」，
// 断言 goal-check 每轮只请求一次（旧实现会在 deactivate 工具执行后再请求一轮），
// 并检查打回理由确实进入主 agent 的下一轮上下文。

type goalCheckTestMode int

const (
	goalCheckRejectThenPass goalCheckTestMode = iota
	goalCheckPassFirst
	goalCheckEmptyThenPass
)

var loadLoopTestTools sync.Once

// 与生产启动链路一致：加载内置工具（deactivate_agent 的拦截逻辑在 PostHook 里）。
func ensureLoopTestTools(t *testing.T) {
	t.Helper()
	loadLoopTestTools.Do(toolsindex.Load)
}

type goalCheckTestServer struct {
	mode goalCheckTestMode

	mu         sync.Mutex
	mainReqs   int
	goalReqs   int
	mainBodies []string
	goalBodies []string
}

func (s *goalCheckTestServer) counts() (main, goal int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mainReqs, s.goalReqs
}

func (s *goalCheckTestServer) bodies() (main, goal []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.mainBodies...), append([]string(nil), s.goalBodies...)
}

func (s *goalCheckTestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)
	system := ""
	if len(req.Messages) > 0 {
		system = req.Messages[0].Content
	}
	isGoalCheck := strings.Contains(system, "internal goal checker")

	s.mu.Lock()
	var idx int
	if isGoalCheck {
		idx = s.goalReqs
		s.goalReqs++
		s.goalBodies = append(s.goalBodies, string(body))
	} else {
		idx = s.mainReqs
		s.mainReqs++
		s.mainBodies = append(s.mainBodies, string(body))
	}
	s.mu.Unlock()

	model := "test-chat"
	if isGoalCheck {
		if s.mode == goalCheckRejectThenPass && idx == 0 {
			writeGoalCheckRejection(w, model)
			return
		}
		writeTextResponse(w, model, "PASS")
		return
	}
	if s.mode == goalCheckEmptyThenPass && idx == 0 {
		// 空最终响应：loop 必须按异常处理并退避重试，而不是直接结束。
		writeTextResponse(w, model, "")
		return
	}
	writeTextResponse(w, model, fmt.Sprintf("attempt %d", idx+1))
}

func chatChunk(model string, delta map[string]any, finish string) map[string]any {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	return map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion.chunk",
		"created": 1,
		"model":   model,
		"choices": []any{choice},
	}
}

func writeSSE(w http.ResponseWriter, chunks []map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for _, c := range chunks {
		data, _ := json.Marshal(c)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func writeTextResponse(w http.ResponseWriter, model, text string) {
	writeSSE(w, []map[string]any{
		chatChunk(model, map[string]any{"role": "assistant", "content": text}, ""),
		chatChunk(model, map[string]any{}, "stop"),
	})
}

func writeGoalCheckRejection(w http.ResponseWriter, model string) {
	writeSSE(w, []map[string]any{
		chatChunk(model, map[string]any{
			"role": "assistant",
			"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_goal_reject", "type": "function",
				"function": map[string]any{"name": "deactivate_agent", "arguments": ""},
			}},
		}, ""),
		chatChunk(model, map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0,
				"function": map[string]any{
					"arguments": `{"prompt":"tests still failing"}`,
				},
			}},
		}, ""),
		chatChunk(model, map[string]any{}, "tool_calls"),
	})
}

func setupGoalCheckLoopTest(t *testing.T, mode goalCheckTestMode) (*goalCheckTestServer, *storageStructs.Chats, <-chan AIResponse, *Object) {
	t.Helper()
	setupConfigForTest()
	ensureLoopTestTools(t)

	srv := &goalCheckTestServer{mode: mode}
	httpSrv := httptest.NewServer(srv)
	t.Cleanup(httpSrv.Close)

	config.GlobalConfig.Model.Models[1] = cfgStruct.ModelConfig{
		ModelName:   "test-chat",
		ModelID:     "test-chat",
		ProviderURL: httpSrv.URL,
		ProviderKey: "test-key",
	}

	db := setupTestDB(t)
	t.Cleanup(func() { _ = u.Unwrap(db.DB()).Close() })
	// goal-check 的全局 PreHook 会读 sub_agents / traces；补齐表结构，避免测试日志刷错误。
	if err := db.AutoMigrate(&storageStructs.SubAgents{}, &storageStructs.Traces{}); err != nil {
		t.Fatalf("migrate goal-check tables: %v", err)
	}
	chat := createTestChat(db, t)
	chat.Root = t.TempDir()
	if err := chat.SaveGoal("all tests pass"); err != nil {
		t.Fatalf("seed goal: %v", err)
	}

	events := make(chan AIResponse, 256)
	loopObj := New(chat)
	loopObj.SetCallback(func(resp AIResponse) {
		select {
		case events <- resp:
		case <-loopObj.Done():
		}
	})
	go loopObj.Start(context.Background())
	t.Cleanup(loopObj.Cancel)
	time.Sleep(100 * time.Millisecond)
	return srv, chat, events, loopObj
}

// waitLoopStop 等待本轮真正结束（StopReasonModel），返回期间收到的全部事件。
func waitLoopStop(t *testing.T, events <-chan AIResponse, timeout time.Duration) []AIResponse {
	t.Helper()
	deadline := time.After(timeout)
	var got []AIResponse
	for {
		select {
		case resp := <-events:
			got = append(got, resp)
			switch resp.StopReason {
			case StopReasonModel:
				return got
			case StopReasonError:
				t.Fatalf("loop error: %v", resp.Error)
			}
		case <-deadline:
			t.Fatalf("timeout waiting for StopReasonModel (events=%d)", len(got))
		}
	}
}

func TestLoopGoalCheckRejectThenPass(t *testing.T) {
	srv, chat, events, loopObj := setupGoalCheckLoopTest(t, goalCheckRejectThenPass)

	if err := loopObj.Chat("do the work", nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	waitLoopStop(t, events, 15*time.Second)

	mainReqs, goalReqs := srv.counts()
	if mainReqs != 2 {
		t.Fatalf("main agent requests = %d, want 2", mainReqs)
	}
	// 打回后必须直接回到主 agent：旧实现会带着校验代理再请求一轮。
	if goalReqs != 2 {
		t.Fatalf("goal-check requests = %d, want 2", goalReqs)
	}
	if _, ok := chat.TemporyDataOfSession[storageStructs.TempKeyGoalCheckRejected]; ok {
		t.Fatal("rejection marker must be consumed")
	}
	if chat.CurrentAgentID != "" {
		t.Fatalf("goal-check agent must be cleared, got %q", chat.CurrentAgentID)
	}
	// 校验通过 = 任务完成：goal 必须清空（内存 + DB）。
	if chat.Goal != "" {
		t.Fatalf("goal must be cleared after task completion, got %q", chat.Goal)
	}
	var persistedGoal string
	if err := chat.DB.Model(&storageStructs.Chats{}).Where("id = ?", chat.ID).
		Pluck("goal", &persistedGoal).Error; err != nil {
		t.Fatalf("query persisted goal: %v", err)
	}
	if persistedGoal != "" {
		t.Fatalf("persisted goal must be cleared after task completion, got %q", persistedGoal)
	}

	mainBodies, goalBodies := srv.bodies()
	if len(goalBodies) < 1 || !strings.Contains(goalBodies[0], "attempt 1") {
		t.Fatalf("goal-check did not receive the main agent's final message: %s", goalBodies)
	}
	// 第二轮主 agent 请求必须带上打回理由（SystemPrompt 末尾注入）。
	if len(mainBodies) < 2 ||
		!strings.Contains(mainBodies[1], "[goal-check rejected]") ||
		!strings.Contains(mainBodies[1], "tests still failing") {
		t.Fatalf("rejection reason not injected into the next main agent request: %s", mainBodies)
	}
}

func TestLoopGoalCheckPassFirstTry(t *testing.T) {
	srv, chat, events, loopObj := setupGoalCheckLoopTest(t, goalCheckPassFirst)

	// 记录 goal 清空广播：任务完成后必须推送空快照，客户端才能清掉旧 goal。
	pushed := make(chan string, 4)
	chat.SetGoalPushFn(func(goal string) {
		select {
		case pushed <- goal:
		default:
		}
	})

	if err := loopObj.Chat("do the work", nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	waitLoopStop(t, events, 15*time.Second)

	mainReqs, goalReqs := srv.counts()
	if mainReqs != 1 || goalReqs != 1 {
		t.Fatalf("requests main=%d goal=%d, want 1/1", mainReqs, goalReqs)
	}
	if chat.CurrentAgentID != "" {
		t.Fatalf("goal-check agent must be cleared after pass, got %q", chat.CurrentAgentID)
	}
	if chat.SystemPrompt != "" {
		t.Fatalf("pass must not inject a rejection notice: %q", chat.SystemPrompt)
	}
	if chat.Goal != "" {
		t.Fatalf("goal must be cleared after pass, got %q", chat.Goal)
	}
	select {
	case goal := <-pushed:
		if goal != "" {
			t.Fatalf("cleared goal push = %q, want empty", goal)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goal clear must be pushed to clients")
	}
}

func TestLoopGoalSessionEmptyResponseRetries(t *testing.T) {
	srv, chat, events, loopObj := setupGoalCheckLoopTest(t, goalCheckEmptyThenPass)

	if err := loopObj.Chat("do the work", nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	got := waitLoopStop(t, events, 15*time.Second)

	gotRetry := false
	for _, resp := range got {
		if resp.Retry != nil && resp.Retry.ErrorText == "empty response" {
			gotRetry = true
		}
	}
	if !gotRetry {
		t.Fatal("empty final response must emit a retry event")
	}
	mainReqs, goalReqs := srv.counts()
	if mainReqs != 2 {
		t.Fatalf("main agent requests = %d, want 2 (empty response retried)", mainReqs)
	}
	if goalReqs != 1 {
		t.Fatalf("goal-check requests = %d, want 1", goalReqs)
	}
	if chat.Goal != "" {
		t.Fatalf("goal must be cleared after retry + pass, got %q", chat.Goal)
	}
}
