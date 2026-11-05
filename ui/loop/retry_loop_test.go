package loop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

func waitLoopStopError(t *testing.T, events <-chan AIResponse, timeout time.Duration) []AIResponse {
	t.Helper()
	deadline := time.After(timeout)
	var got []AIResponse
	for {
		select {
		case resp := <-events:
			got = append(got, resp)
			switch resp.StopReason {
			case StopReasonError:
				return got
			case StopReasonModel:
				t.Fatal("loop finished successfully, want StopReasonError")
			}
		case <-deadline:
			t.Fatalf("timeout waiting for StopReasonError (events=%d)", len(got))
		}
	}
}

// 未设置 goal 的会话必须保持 3 次重试上限（首次请求 + 3 次重试）。
func TestLoopRetryLimitedWithoutGoal(t *testing.T) {
	setupConfigForTest()
	ensureLoopTestTools(t)

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "temporary failure", http.StatusInternalServerError)
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

	events := make(chan AIResponse, 64)
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

	if err := loopObj.Chat("do the work", nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	got := waitLoopStopError(t, events, 20*time.Second)

	if n := requests.Load(); n != int32(maxRetries+1) {
		t.Fatalf("requests = %d, want %d", n, maxRetries+1)
	}
	retries := 0
	for _, resp := range got {
		if resp.Retry != nil {
			retries++
		}
	}
	if retries != maxRetries {
		t.Fatalf("retry events = %d, want %d", retries, maxRetries)
	}
}

// goal 会话虽然无限重试，但余额/额度类错误必须立刻终止，不能永久空转。
func TestLoopGoalFatalErrorStopsRetry(t *testing.T) {
	setupConfigForTest()
	ensureLoopTestTools(t)

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			http.Error(w, "temporary failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"Insufficient Balance"}}`))
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
	if err := db.AutoMigrate(&storageStructs.SubAgents{}, &storageStructs.Traces{}); err != nil {
		t.Fatalf("migrate loop test tables: %v", err)
	}
	chat := createTestChat(db, t)
	chat.Root = t.TempDir()
	chat.Goal = "all tests pass"

	events := make(chan AIResponse, 64)
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

	if err := loopObj.Chat("do the work", nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	got := waitLoopStopError(t, events, 20*time.Second)

	if n := requests.Load(); n != 2 {
		t.Fatalf("requests = %d, want 2 (one 500 retry, then fatal stop)", n)
	}
	retries := 0
	for _, resp := range got {
		if resp.Retry != nil {
			retries++
		}
	}
	if retries != 1 {
		t.Fatalf("retry events = %d, want 1", retries)
	}
	var lastErr string
	for _, resp := range got {
		if resp.StopReason == StopReasonError && resp.Error != nil {
			lastErr = resp.Error.Error()
		}
	}
	if !strings.Contains(lastErr, "Insufficient Balance") {
		t.Fatalf("final error = %q, want Insufficient Balance", lastErr)
	}
}
