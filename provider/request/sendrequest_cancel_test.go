package request

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/stats"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCancelTestConfig(modelID int32, modelName string) {
	config.GlobalConfig.Model.Models = make(map[int32]cfgStruct.ModelConfig)
	config.GlobalConfig.Model.Models[modelID] = cfgStruct.ModelConfig{
		ModelID:     modelName,
		ProviderURL: openai.BaseURL,
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
	// 隔离全局 token 用量统计
	stats.ResetForTest()
	stats.SetFilePath(filepath.Join(t.TempDir(), "usage.json"))
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
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     true,
		EnableScopes:   make(map[string]bool),
	}

	ctx, cancel := context.WithCancel(context.Background())

	var receivedDeltas []string
	var mu sync.Mutex
	// firstDelta 在收到第一个非空增量时关闭：取消时机由此驱动，而不是靠固定 sleep
	firstDelta := make(chan struct{})
	var firstDeltaOnce sync.Once

	errCh := make(chan error, 1)

	go func() {
		_, err := SendRequest(ctx, session,
			func(delta, thinking string, _ uint64, _ structs.Usage, _ *string) error {
				mu.Lock()
				receivedDeltas = append(receivedDeltas, delta)
				// 收到断言依赖的 "mock" 后再取消：过早取消会连这个词都还没到，
				// 用例断言持久化内容包含 "mock" 就会失败。
				ready := strings.Contains(strings.Join(receivedDeltas, ""), "mock")
				mu.Unlock()
				if ready {
					firstDeltaOnce.Do(func() { close(firstDelta) })
				}
				return nil
			})
		errCh <- err
	}()

	// 取消时机改为"收到首个增量后立即取消"：此前固定 sleep 250ms，在 -race 等
	// 慢速环境下 mock 的流式响应可能先跑完，用例就测不到"中途取消"（实测 -race 偶发失败）。
	select {
	case <-firstDelta:
	case err := <-errCh:
		t.Fatalf("SendRequest 在收到任何增量前就结束了（err=%v）", err)
	case <-time.After(5 * time.Second):
		t.Fatal("等待首个流式增量超时")
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

	// 等待异步持久化 goroutine 完成：轮询而不是固定 sleep（固定等待在慢机器上
	// 会假失败、在快机器上白等）。
	var savedMsg storageStructs.Messages
	persistDeadline := time.Now().Add(5 * time.Second)
	for {
		err = db.Where("chat_id = ? AND type = ?", chat.ID, storageStructs.MessagesRoleAgent).
			Order("id DESC").
			First(&savedMsg).Error
		if err == nil && savedMsg.Delta != "" {
			break
		}
		if time.Now().After(persistDeadline) {
			t.Fatalf("超时等待取消后的内容持久化（last err=%v, delta=%q）", err, savedMsg.Delta)
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Logf("Persisted delta length: %d", len(savedMsg.Delta))
	t.Logf("Persisted delta: %q", savedMsg.Delta)

	// 验证内容不为空
	if savedMsg.Delta == "" {
		t.Fatal("Persisted delta is empty - content was lost after cancel")
	}

	// 验证内容包含预期文本（只检查首词"mock"，避免不同平台调度时序差异）
	if !strings.Contains(savedMsg.Delta, "mock") {
		t.Errorf("Persisted delta does not contain expected mock content: %q", savedMsg.Delta)
	}

	// 验证持久化内容短于完整响应（证明确实在中途取消了）。
	// 旧实现只 t.Logf 一条警告，"取消其实没生效、请求已经跑完"也能通过——
	// 那样这个用例就完全失去意义，因此改为硬失败。
	fullResponse := "This is a mock response from model test-chat. Your message was received and processed."
	if len(savedMsg.Delta) >= len(fullResponse) {
		t.Fatalf("取消未生效：持久化内容 %d 字节 >= 完整响应 %d 字节（delta=%q）",
			len(savedMsg.Delta), len(fullResponse), savedMsg.Delta)
	}

	if savedMsg.ModelID != 1 {
		t.Errorf("Expected ModelID 1, got %d", savedMsg.ModelID)
	}
}

// TestSendRequest_ContextCancel_ImmediateReturn 测试预取消 context 下 SendRequest 立即返回
func TestSendRequest_ContextCancel_ImmediateReturn(t *testing.T) {
	// 隔离全局 token 用量统计
	stats.ResetForTest()
	stats.SetFilePath(filepath.Join(t.TempDir(), "usage.json"))
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
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     true,
		EnableScopes:   make(map[string]bool),
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
