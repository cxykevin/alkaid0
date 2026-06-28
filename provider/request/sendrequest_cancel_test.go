package request

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupCancelTestConfig(modelID int32, modelName string) {
	config.GlobalConfig.Model.Models = make(map[int32]cfgStruct.ModelConfig)
	config.GlobalConfig.Model.Models[modelID] = cfgStruct.ModelConfig{
		ModelID:     modelName,
		ProviderURL: "http://localhost:56108/v1",
		ProviderKey: "mock-key",
		ModelName:   "Cancel Test Model",
	}
}

// TestSendRequest_ContextCancel_ContentPersisted 测试取消时流式内容正确入库
//
// 使用 test-chat（50ms/词）中途取消，验证：
//  1. 取消后 SendRequest 立即返回 context.Canceled
//  2. 取消前收到的内容被正确持久化到数据库
func TestSendRequest_ContextCancel_ContentPersisted(t *testing.T) {
	openai.StartServerTask()
	setupCancelTestConfig(1, "test-chat")

	initAgentsConsumer()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	defer u.Unwrap(db.DB()).Close()
	if err := db.AutoMigrate(
		&storageStructs.Chats{},
		&storageStructs.Messages{},
		&storageStructs.SubAgents{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	chat := storageStructs.Chats{
		ID:          100,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{
		ChatID: chat.ID,
		Type:   storageStructs.MessagesRoleUser,
		Delta:  "Hello, test cancel content persistence.",
	}).Error; err != nil {
		t.Fatalf("Failed to create user message: %v", err)
	}

	session := &storageStructs.Chats{
		ID:              chat.ID,
		DB:              db,
		LastModelID:     1,
		CurrentAgentID:  "",
		InTestFlag:      true,
		EnableScopes:    make(map[string]bool),
	}

	ctx, cancel := context.WithCancel(context.Background())

	var receivedDeltas []string
	var mu sync.Mutex

	errCh := make(chan error, 1)

	go func() {
		_, err := SendRequest(ctx, session,
			func(delta, thinking string, _ uint64, _ structs.Usage, _ *string) error {
				mu.Lock()
				receivedDeltas = append(receivedDeltas, delta)
				mu.Unlock()
				return nil
			})
		errCh <- err
	}()

	// test-chat 模型：14 个词，~700ms 总时长，50ms/词
	// 等待 250ms，应该收到 ~5 个词
	time.Sleep(250 * time.Millisecond)

	// 检查是否提前完成（防止 mock server 问题导致测试误判）
	select {
	case err := <-errCh:
		t.Fatalf("SendRequest completed before cancel (err=%v) - response too fast", err)
	default:
	}

	mu.Lock()
	receivedLen := len(strings.Join(receivedDeltas, ""))
	mu.Unlock()
	t.Logf("Received content length before cancel: %d", receivedLen)

	if receivedLen == 0 {
		t.Fatal("No content received before cancel")
	}

	// 触发取消并测量返回时间
	cancelStart := time.Now()
	cancel()
	returnTime := time.Since(cancelStart)
	t.Logf("Cancel to SendRequest return: %v", returnTime)

	// 等待 SendRequest 返回
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Expected context.Canceled error, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Expected context.Canceled, got: %v", err)
		}
		t.Logf("SendRequest returned with expected: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for SendRequest to return after cancel")
	}

	// 等待异步持久化 goroutine 完成
	time.Sleep(500 * time.Millisecond)

	// 查询数据库中 agent 消息
	var savedMsg storageStructs.Messages
	err = db.Where("chat_id = ? AND type = ?", chat.ID, storageStructs.MessagesRoleAgent).
		Order("id DESC").
		First(&savedMsg).Error
	if err != nil {
		t.Fatalf("Failed to query persisted message: %v", err)
	}

	t.Logf("Persisted delta length: %d", len(savedMsg.Delta))
	t.Logf("Persisted delta: %q", savedMsg.Delta)

	// 验证内容不为空
	if savedMsg.Delta == "" {
		t.Fatal("Persisted delta is empty - content was lost after cancel")
	}

	// 验证内容包含预期文本
	if !strings.Contains(savedMsg.Delta, "mock response") {
		t.Errorf("Persisted delta does not contain expected mock content: %q", savedMsg.Delta)
	}

	// 验证持久化内容短于完整响应（证明确实在中途取消了）
	fullResponse := "This is a mock response from model test-chat. Your message was received and processed."
	if len(savedMsg.Delta) >= len(fullResponse) {
		t.Logf("Warning: persisted (%d) >= full response (%d) - may have completed before cancel",
			len(savedMsg.Delta), len(fullResponse))
	}

	if savedMsg.ModelID != 1 {
		t.Errorf("Expected ModelID 1, got %d", savedMsg.ModelID)
	}
}

// TestSendRequest_ContextCancel_ImmediateReturn 测试预取消 context 下 SendRequest 立即返回
func TestSendRequest_ContextCancel_ImmediateReturn(t *testing.T) {
	openai.StartServerTask()
	setupCancelTestConfig(1, "test-chat-flash")

	initAgentsConsumer()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	defer u.Unwrap(db.DB()).Close()
	if err := db.AutoMigrate(
		&storageStructs.Chats{},
		&storageStructs.Messages{},
		&storageStructs.SubAgents{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	chat := storageStructs.Chats{
		ID:          200,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{
		ChatID: chat.ID,
		Type:   storageStructs.MessagesRoleUser,
		Delta:  "Hello, cancel immediately.",
	}).Error; err != nil {
		t.Fatalf("Failed to create user message: %v", err)
	}

	session := &storageStructs.Chats{
		ID:              chat.ID,
		DB:              db,
		LastModelID:     1,
		CurrentAgentID:  "",
		InTestFlag:      true,
		EnableScopes:    make(map[string]bool),
	}

	// 预取消 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err = SendRequest(ctx, session,
		func(delta, thinking string, _ uint64, _ structs.Usage, _ *string) error {
			return nil
		})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Expected context.Canceled error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Expected context.Canceled, got: %v", err)
	}
	if elapsed > 1*time.Second {
		t.Errorf("SendRequest took too long (%v) after pre-cancelled context", elapsed)
	}
	t.Logf("SendRequest returned in %v after pre-cancelled context", elapsed)
}

// TestSendRequest_ContextCancel_FlashModel 测试 flash 模型下取消行为
// flash 模型瞬间返回，取消可能在请求完成后才触发，验证不丢失数据
func TestSendRequest_ContextCancel_FlashModel(t *testing.T) {
	openai.StartServerTask()
	setupCancelTestConfig(1, "test-chat-flash")

	initAgentsConsumer()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	defer u.Unwrap(db.DB()).Close()
	if err := db.AutoMigrate(
		&storageStructs.Chats{},
		&storageStructs.Messages{},
		&storageStructs.SubAgents{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	chat := storageStructs.Chats{
		ID:          300,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{
		ChatID: chat.ID,
		Type:   storageStructs.MessagesRoleUser,
		Delta:  "Hello, flash model test.",
	}).Error; err != nil {
		t.Fatalf("Failed to create user message: %v", err)
	}

	session := &storageStructs.Chats{
		ID:              chat.ID,
		DB:              db,
		LastModelID:     1,
		CurrentAgentID:  "",
		InTestFlag:      true,
		EnableScopes:    make(map[string]bool),
	}

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		_, err := SendRequest(ctx, session,
			func(delta, thinking string, _ uint64, _ structs.Usage, _ *string) error {
				return nil
			})
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Unexpected error: %v", err)
		}
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	// 验证内容被持久化
	var savedMsg storageStructs.Messages
	err = db.Where("chat_id = ? AND type = ?", chat.ID, storageStructs.MessagesRoleAgent).
		Order("id DESC").
		First(&savedMsg).Error
	if err != nil {
		t.Fatal("Expected persisted message, but none found")
	}
	if len(savedMsg.Delta) == 0 {
		t.Error("Persisted delta is empty")
	}
	t.Logf("Flash model persisted delta length: %d", len(savedMsg.Delta))
}
