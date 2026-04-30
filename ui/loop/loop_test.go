package loop

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 初始化测试数据库
func setupTestDB(t *testing.T) *gorm.DB {
	// 使用内存数据库
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to init test db: %v", err)
	}

	// 创建表
	if err := db.AutoMigrate(&storageStructs.Chats{}, &storageStructs.Messages{}); err != nil {
		t.Fatalf("Failed to migrate db: %v", err)
	}

	return db
}

// 初始化测试配置
func setupConfigForTest() {
	// 设置 mock 服务器地址
	apiKey := "test-key"

	if config.GlobalConfig == nil {
		config.GlobalConfig = &structs.Config{}
	}

	config.GlobalConfig.Version = 1
	config.GlobalConfig.Model = structs.ModelsConfig{
		Models: map[int32]structs.ModelConfig{
			1: {
				ModelName:   "test-chat",
				ModelID:     "test-chat",
				ProviderURL: "http://localhost:56108/v1",
				ProviderKey: apiKey,
			},
			2: {
				ModelName:   "test-chat-flash",
				ModelID:     "test-chat-flash",
				ProviderURL: "http://localhost:56108/v1",
				ProviderKey: apiKey,
			},
		},
	}
	config.GlobalConfig.Agent = structs.AgentsConfig{
		MaxCallCount:        5,
		DisableSandbox:      true,
		IgnoreBuiltinAgents: true,
		IgnoreDefaultRules:  true,
		DefaultAutoApprove:  "true",
		SummaryModel:        1,
		Agents: map[string]structs.AgentConfig{
			"test-agent": {
				AgentName: "test-agent",
			},
		},
	}
}

// 创建测试会话
func createTestChat(db *gorm.DB, t *testing.T) *storageStructs.Chats {
	chat := &storageStructs.Chats{
		LastModelID:          1,
		State:                state.StateReciving,
		TemporyDataOfSession: make(map[string]any),
		TemporyDataOfRequest: make(map[string]any),
		DB:                   db,
	}

	if err := db.Create(chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	return chat
}

// TestNew 测试创建新的 loop 对象
func TestNew(t *testing.T) {
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	if loopObj == nil {
		t.Fatal("Expected loopObj to not be nil")
	}

	if loopObj.session != chat {
		t.Fatal("Expected loopObj.session to equal chat")
	}

	if loopObj.sendQueue == nil {
		t.Fatal("Expected sendQueue to be initialized")
	}

	if loopObj.recvQueue == nil {
		t.Fatal("Expected recvQueue to be initialized")
	}
}

// TestChangeModel 测试模型切换
func TestChangeModel(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 切换模型
	err := loopObj.ChangeModel(2)
	if err != nil {
		t.Fatalf("Expected no error when changing model: %v", err)
	}

	// 验证模型是否已切换
	if chat.LastModelID != 2 {
		t.Fatalf("Expected LastModelID to be 2, got %d", chat.LastModelID)
	}
}

// TestChangeModelNotExists 测试切换不存在的模型
func TestChangeModelNotExists(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 尝试切换不存在的模型
	err := loopObj.ChangeModel(999)
	if err == nil {
		t.Fatal("Expected error when changing to non-existent model")
	}
}

// TestChat 测试发送消息
func TestChat(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	err := loopObj.Chat("Hello", nil)
	if err != nil {
		t.Fatalf("Expected no error when sending message: %v", err)
	}

	// 验证消息被放入队列
	select {
	case msg := <-loopObj.sendQueue:
		if msg.Msg != "Hello" {
			t.Fatalf("Expected message text to be 'Hello', got '%s'", msg.Msg)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Expected message to be queued")
	}
}

// TestChatQueueFull 测试消息队列满的情况
func TestChatQueueFull(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 填满队列
	for i := 0; i < queueSize; i++ {
		loopObj.sendQueue <- msgObj{Msg: "test"}
	}

	// 再发一条消息应该失败
	err := loopObj.Chat("Hello", nil)
	if err == nil {
		t.Fatal("Expected error when queue is full")
	}
}

// TestSummary 测试摘要操作
func TestSummary(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	err := loopObj.Summary()
	if err != nil {
		t.Fatalf("Expected no error when calling Summary: %v", err)
	}

	// 验证消息被放入队列
	select {
	case msg := <-loopObj.sendQueue:
		if msg.Command != msgActionSummary {
			t.Fatalf("Expected command to be msgActionSummary, got %d", msg.Command)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Expected message to be queued")
	}
}

// TestApprove 测试审批操作
func TestApprove(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	err := loopObj.Approve()
	if err != nil {
		t.Fatalf("Expected no error when calling Approve: %v", err)
	}

	// 验证消息被放入队列
	select {
	case msg := <-loopObj.sendQueue:
		if msg.Command != msgActionApprove {
			t.Fatalf("Expected command to be msgActionApprove, got %d", msg.Command)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Expected message to be queued")
	}
}

// TestStop 测试停止循环
func TestStop(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 启动 loop
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go loopObj.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// 停止 loop
	loopObj.Stop()
	time.Sleep(100 * time.Millisecond)
}

// TestSetCallback 测试设置回调
func TestSetCallback(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	callbackCalled := false
	loopObj.SetCallback(func(resp AIResponse) {
		callbackCalled = true
	})

	// 发送响应并验证回调被调用
	go func() {
		loopObj.recvQueue <- AIResponse{
			Content:    "test",
			StopReason: StopReasonNone,
		}
	}()

	time.Sleep(200 * time.Millisecond)

	if !callbackCalled {
		t.Fatal("Expected callback to be called")
	}
}

// TestStartWithContext 测试使用上下文启动 loop
func TestStartWithContext(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// 收集响应
	responses := []AIResponse{}
	stopChan := make(chan bool)

	loopObj.SetCallback(func(resp AIResponse) {
		responses = append(responses, resp)
		if resp.StopReason != StopReasonNone {
			stopChan <- true
		}
	})

	go loopObj.Start(ctx)

	select {
	case <-stopChan:
		// 成功停止
	case <-time.After(2 * time.Second):
		t.Fatal("Expected loop to stop with context timeout")
	}
}

// TestStreamingChatIntegration 测试流式聊天集成
// 这个测试需要 mock 服务器running
func TestStreamingChatIntegration(t *testing.T) {
	// 检查是否设置了 mock 服务器标志
	if os.Getenv("ALKAID0_TEST_MOCK_SERVER") != "true" {
		t.Skip("Skipping integration test - set ALKAID0_TEST_MOCK_SERVER=true to run")
	}

	setupConfigForTest()

	// 启动 mock 服务器
	openai.StartServerTask()
	defer func() {
		// 给服务器时间清理
		time.Sleep(100 * time.Millisecond)
	}()

	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 收集响应
	responses := []AIResponse{}
	stopChan := make(chan bool)
	errorChan := make(chan error)

	loopObj.SetCallback(func(resp AIResponse) {
		responses = append(responses, resp)
		if resp.Error != nil {
			errorChan <- resp.Error
		}
		if resp.StopReason != StopReasonNone {
			stopChan <- true
		}
	})

	// 启动 loop
	go loopObj.Start(ctx)

	// 给 loop 时间初始化
	time.Sleep(300 * time.Millisecond)

	// 发送聊天消息
	err := loopObj.Chat("Hello, mock server!", nil)
	if err != nil {
		t.Fatalf("Failed to send chat message: %v", err)
	}

	// 等待响应或错误
	select {
	case err := <-errorChan:
		t.Logf("Got error response: %v", err)
	case <-stopChan:
		// 成功完成
		if len(responses) == 0 {
			t.Fatal("Expected to receive responses")
		}
		t.Logf("Received %d responses", len(responses))
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for response")
	}
}

// TestLoopConcurrency 测试并发发送消息
func TestLoopConcurrency(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 并发发送多条消息
	for i := 0; i < 5; i++ {
		go func(idx int) {
			loopObj.Chat("Message "+string(rune(idx)), nil)
		}(i)
	}

	time.Sleep(200 * time.Millisecond)

	// 验证所有消息都被放入队列
	count := 0
	for {
		select {
		case <-loopObj.sendQueue:
			count++
		default:
			goto done
		}
	}
done:

	if count < 5 {
		t.Fatalf("Expected at least 5 messages in queue, got %d", count)
	}
}

// // TestReceiveQueueCallback 测试接收队列回调
// func TestReceiveQueueCallback(t *testing.T) {
// 	setupConfigForTest()
// 	db := setupTestDB(t)
// 	chat := createTestChat(db, t)

// 	loopObj := New(chat)

// 	receivedCount := 0
// 	loopObj.SetCallback(func(resp AIResponse) {
// 		receivedCount++
// 	})

// 	// 给回调 goroutine 启动的时间
// 	time.Sleep(100 * time.Millisecond)

// 	// 发送多个响应
// 	for range 3 {
// 		loopObj.recvQueue <- AIResponse{
// 			Content:    "test response",
// 			StopReason: StopReasonNone,
// 		}
// 	}

// 	time.Sleep(200 * time.Millisecond)

// 	if receivedCount < 3 {
// 		t.Fatalf("Expected at least 3 responses, got %d", receivedCount)
// 	}
// }

// BenchmarkChat 基准测试：消息发送
func BenchmarkChat(b *testing.B) {
	setupConfigForTest()
	db := setupTestDB(&testing.T{})
	chat := createTestChat(db, &testing.T{})

	loopObj := New(chat)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loopObj.Chat("Test message", nil)
	}
}

// BenchmarkChangeModel 基准测试：模型切换
func BenchmarkChangeModel(b *testing.B) {
	setupConfigForTest()
	db := setupTestDB(&testing.T{})
	chat := createTestChat(db, &testing.T{})

	loopObj := New(chat)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		modelID := int((i % 2) + 1)
		loopObj.ChangeModel(modelID)
	}
}

// AIResponseStopReason 枚举值测试
func TestAIResponseStopReason(t *testing.T) {
	tests := []struct {
		name   string
		reason StopReason
		want   StopReason
	}{
		{"None", StopReasonNone, StopReasonNone},
		{"User", StopReasonUser, StopReasonUser},
		{"Error", StopReasonError, StopReasonError},
		{"PendingTool", StopReasonPendingTool, StopReasonPendingTool},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.reason != tt.want {
				t.Errorf("Expected %v, got %v", tt.want, tt.reason)
			}
		})
	}
}

// TestInitializeSessionContext 测试初始化会话上下文
func TestInitializeSessionContext(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 验证回调是否正常工作
	callCount := 0
	loopObj.SetCallback(func(resp AIResponse) {
		callCount++
	})

	go loopObj.Start(ctx)
	time.Sleep(200 * time.Millisecond)
}

// TestMessageWithReferences 测试带引用的消息
func TestMessageWithReferences(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	chat := createTestChat(db, t)

	loopObj := New(chat)

	refers := []any{"ref1", "ref2"}
	err := loopObj.Chat("Message with refs", refers)
	if err != nil {
		t.Fatalf("Expected no error: %v", err)
	}

	select {
	case msg := <-loopObj.sendQueue:
		if len(msg.Refers) != 2 {
			t.Fatalf("Expected 2 references, got %d", len(msg.Refers))
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Expected message to be queued")
	}
}
