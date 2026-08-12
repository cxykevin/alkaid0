package request

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/stats"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// setupUsageTestConfig 配置一个指向 url 的提示词模式模型（非原生 tool_calls），
// 供全局 token 用量统计的集成测试使用。
func setupUsageTestConfig(url string) {
	config.GlobalConfigSwap(cfgStruct.Config{
		Model: cfgStruct.ModelsConfig{
			DefaultModelID: 1,
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName:         "usage-test",
					ModelID:           "usage-test",
					ProviderURL:       url,
					ProviderKey:       "mock-key",
					EnableToolCalling: false,
					EnableThinking:    false,
				},
			},
		},
		Agent: cfgStruct.AgentsConfig{
			GlobalPrompt: "You are a helpful assistant",
		},
	})
}

// TestSendRequest_RecordsNestedCachedTokens 验证缓存命中 token 位于 OpenAI 标准
// prompt_tokens_details.cached_tokens / CCR 网关的 billing_usage.claude_usage.cache_read_input_tokens
// 时也能被正确累计到全局统计（覆盖 claude-code-router 类网关的 usage 格式）。
func TestSendRequest_RecordsNestedCachedTokens(t *testing.T) {
	stats.ResetForTest()
	defer stats.ResetForTest()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-usage-nested", Model: "usage-test",
			Choices: []structs.Choice{{Index: 0, Delta: structs.Message{Role: structs.RoleAssistant, Content: "hi"}}},
		})
		// usage 帧：OpenAI 标准嵌套缓存 + CCR Anthropic 计费明细（同一命中值 150）
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-usage-nested", Model: "usage-test",
			Usage: &structs.Usage{
				PromptTokens:       200,
				CompletionTokens:   40,
				TotalTokens:        240,
				PromptTokensDetails: &structs.PromptTokensDetails{CachedTokens: 150},
				BillingUsage:        &structs.BillingUsage{ClaudeUsage: &structs.ClaudeUsage{CacheReadInputTokens: 150}},
			},
		})
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupUsageTestConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{ID: 9102, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "hi"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}

	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     false,
		EnableScopes:   make(map[string]bool),
	}
	if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	snap := stats.Snapshot()
	if snap.Total.Requests != 1 {
		t.Fatalf("expected 1 recorded request, got %d", snap.Total.Requests)
	}
	if snap.Total.CachedTokens != 150 {
		t.Fatalf("expected nested cached tokens 150, got %d", snap.Total.CachedTokens)
	}
	if snap.Total.CacheHitRatio != 0.75 {
		t.Fatalf("expected cache ratio 0.75, got %v", snap.Total.CacheHitRatio)
	}
}

// TestSendRequest_RecordsUsage 验证正常对话请求后 token 用量被累计到全局统计：
// 输入/输出/缓存 token 分别正确累加，缓存比正确计算，分模型统计正确。
func TestSendRequest_RecordsUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-usage", Model: "usage-test",
			Choices: []structs.Choice{{Index: 0, Delta: structs.Message{Role: structs.RoleAssistant, Content: "hello"}}},
		})
		// usage 帧（choices 为空，仅携带用量）
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-usage", Model: "usage-test",
			Usage: &structs.Usage{PromptTokens: 100, CompletionTokens: 50, CachedTokens: 30, TotalTokens: 150},
		})
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupUsageTestConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{ID: 9100, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "hi"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}

	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     false,
		EnableScopes:   make(map[string]bool),
	}
	if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
		t.Fatalf("SendRequest: %v", err)
	}

	snap := stats.Snapshot()
	if snap.Total.Requests != 1 {
		t.Fatalf("expected 1 recorded request, got %d", snap.Total.Requests)
	}
	if snap.Total.PromptTokens != 100 || snap.Total.CompletionTokens != 50 || snap.Total.CachedTokens != 30 {
		t.Fatalf("unexpected total usage: %+v", snap.Total)
	}
	if snap.Total.CacheHitRatio != 0.3 {
		t.Fatalf("expected total cache ratio 0.3, got %v", snap.Total.CacheHitRatio)
	}
	if len(snap.Models) != 1 {
		t.Fatalf("expected 1 model in stats, got %d", len(snap.Models))
	}
	m := snap.Models[0]
	if m.ModelID != 1 || m.ModelName != "usage-test" {
		t.Fatalf("unexpected model identity: %+v", m)
	}
	if m.PromptTokens != 100 || m.CompletionTokens != 50 || m.CachedTokens != 30 || m.Requests != 1 {
		t.Fatalf("unexpected model usage: %+v", m)
	}
	if m.CacheHitRatio != 0.3 {
		t.Fatalf("expected model cache ratio 0.3, got %v", m.CacheHitRatio)
	}
}

// TestSendRequest_ModelNotFound_NotCounted 验证模型不存在时请求被拒绝且不计入全局统计。
func TestSendRequest_ModelNotFound_NotCounted(t *testing.T) {
	config.GlobalConfigSwap(cfgStruct.Config{}) // 清空模型配置，保证 999 不存在
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{ID: 9101, LastModelID: 999}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    999,
		CurrentAgentID: "",
		EnableScopes:   make(map[string]bool),
	}
	if _, err := SendRequest(context.Background(), session, noopCallback); err == nil {
		t.Fatal("expected 'model not found' error, got nil")
	}

	snap := stats.Snapshot()
	if snap.Total.Requests != 0 {
		t.Fatalf("model-not-found request should not be counted, got requests=%d", snap.Total.Requests)
	}
	if len(snap.Models) != 0 {
		t.Fatalf("model-not-found request should not add model stats, got %+v", snap.Models)
	}
}
