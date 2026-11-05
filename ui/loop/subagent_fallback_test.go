package loop

import (
	"context"
	"encoding/json"
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
	"github.com/cxykevin/alkaid0/ui/funcs"
	u "github.com/cxykevin/alkaid0/utils"
)

// 普通子代理（非 goal-check）未调用 deactivate_agent 就结束：loop 必须把最后一段输出
// 回传给主 agent 并继续，而不是让会话卡在子代理状态。
func TestLoopSubagentWithoutDeactivateFallsBack(t *testing.T) {
	setupConfigForTest()
	ensureLoopTestTools(t)

	var mu sync.Mutex
	subagentReqs, mainReqs := 0, 0
	const subagentMarker = "you are a test subagent"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		system := ""
		if len(req.Messages) > 0 {
			system = req.Messages[0].Content
		}
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(strings.ToLower(system), subagentMarker) {
			subagentReqs++
			writeTextResponse(w, "test-chat", "partial work")
			return
		}
		mainReqs++
		writeTextResponse(w, "test-chat", "final answer")
	}))
	t.Cleanup(srv.Close)

	config.GlobalConfig.Model.Models[1] = cfgStruct.ModelConfig{
		ModelName:   "test-chat",
		ModelID:     "test-chat",
		ProviderURL: srv.URL,
		ProviderKey: "test-key",
	}

	db := setupTestDB(t)
	t.Cleanup(func() { _ = u.Unwrap(db.DB()).Close() })
	// 请求构建时的全局 PreHook 会读 sub_agents / traces，补齐表结构避免刷错误日志。
	if err := db.AutoMigrate(&storageStructs.SubAgents{}, &storageStructs.Traces{}); err != nil {
		t.Fatalf("migrate loop test tables: %v", err)
	}
	chat := createTestChat(db, t)
	chat.Root = t.TempDir()

	// 用户消息必须在没有子代理时写入：UserAddMsg 会把活跃子代理强制停用，
	// 那样就模拟不出「子代理未 deactivate 就结束」的场景。
	msgID, err := funcs.UserAddMsgWithID(chat, "do the work", nil)
	if err != nil {
		t.Fatalf("UserAddMsgWithID: %v", err)
	}
	chat.CurrentAgentID = "worker"
	chat.NowAgent = "worker"
	chat.CurrentAgentConfig = cfgStruct.AgentConfig{AgentPrompt: "You are a test subagent"}

	events := make(chan AIResponse, 128)
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

	if err := loopObj.ChatWithID("do the work", msgID, nil); err != nil {
		t.Fatalf("ChatWithID: %v", err)
	}
	waitLoopStop(t, events, 15*time.Second)

	mu.Lock()
	gotSub, gotMain := subagentReqs, mainReqs
	mu.Unlock()
	if gotSub != 1 || gotMain != 1 {
		t.Fatalf("requests subagent=%d main=%d, want 1/1", gotSub, gotMain)
	}
	if chat.CurrentAgentID != "" || chat.NowAgent != "" {
		t.Fatalf("subagent state not cleared: agent=%q now=%q", chat.CurrentAgentID, chat.NowAgent)
	}

	var msgs []storageStructs.Messages
	if err := db.Where("chat_id = ? AND type = ?", chat.ID, storageStructs.MessagesRoleCommunicate).
		Order("id ASC").Find(&msgs).Error; err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 ||
		!strings.Contains(msgs[0].Delta, "[subagent worker returned without deactivate]") ||
		!strings.Contains(msgs[0].Delta, "partial work") {
		t.Fatalf("unexpected fallback message: %#v", msgs)
	}
}
