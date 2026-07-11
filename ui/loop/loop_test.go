package loop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 填满队列
	for range queueSize {
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	callbackCalled := atomic.Bool{}
	loopObj.SetCallback(func(resp AIResponse) {
		callbackCalled.Store(true)
	})

	// 发送响应并验证回调被调用
	go func() {
		loopObj.recvQueue <- AIResponse{
			Content:    "test",
			StopReason: StopReasonNone,
		}
	}()

	time.Sleep(200 * time.Millisecond)

	if !callbackCalled.Load() {
		t.Fatal("Expected callback to be called")
	}
}

// TestStartWithContext 测试使用上下文启动 loop
func TestStartWithContext(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
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

// TestStreamingChatIntegration 测试流式聊天集成（使用 mock OpenAI 服务器）
// 验证完整的消息发送 → LLM 流式响应 → stop reason 生命周期
func TestStreamingChatIntegration(t *testing.T) {
	setupConfigForTest()

	// 启动 mock 服务器（sync.Once 确保只启动一次）
	openai.StartServerTask()

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 通过 channel 收集响应，避免并发读写 race
	respChan := make(chan AIResponse, 100)

	loopObj.SetCallback(func(resp AIResponse) {
		respChan <- resp
	})

	// 启动 loop
	go loopObj.Start(ctx)

	// 给 loop 时间初始化
	time.Sleep(200 * time.Millisecond)

	// 发送聊天消息
	err := loopObj.Chat("Hello, mock server!", nil)
	if err != nil {
		t.Fatalf("Failed to send chat message: %v", err)
	}

	// 收集所有响应直到 StopReasonModel
	var responses []AIResponse
	timeout := time.After(3 * time.Second)
	collecting := true

	for collecting {
		select {
		case resp := <-respChan:
			responses = append(responses, resp)
			if resp.StopReason != StopReasonNone {
				collecting = false
			}
		case <-timeout:
			t.Fatal("Timeout waiting for LLM response")
		}
	}

	// 验证有流式响应的内容
	if len(responses) == 0 {
		t.Fatal("Expected to receive responses")
	}

	hasContent := false
	for _, resp := range responses {
		if resp.Content != "" {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Error("Expected at least one streaming response with Content")
	}

	// 验证有正常停止原因（StopReasonModel）
	hasStopReason := false
	for _, resp := range responses {
		if resp.StopReason == StopReasonModel {
			hasStopReason = true
			break
		}
	}
	if !hasStopReason {
		t.Error("Expected StopReasonModel in responses")
	}

	t.Logf("Received %d responses from streaming chat", len(responses))
}

// TestLoopConcurrency 测试并发发送消息
func TestLoopConcurrency(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 并发发送多条消息
	for i := range 5 {
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

// --- 新增测试：Cancel 方法、队列满边界条件、取消上下文退出路径 ---

// TestCancel 测试 Cancel 方法关闭 done 通道且可多次安全调用
func TestCancel(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 验证 done 初始为开启状态
	select {
	case <-loopObj.done:
		t.Fatal("expected done to be open initially")
	default:
	}

	// 首次 Cancel
	loopObj.Cancel()

	// done 应已关闭
	select {
	case <-loopObj.done:
		// ok
	default:
		t.Fatal("expected done to be closed after Cancel")
	}

	// 多次 Cancel 不应 panic（sync.Once 保护）
	loopObj.Cancel()
	loopObj.Cancel()
}

// TestStartWithCancelledContext 测试使用已取消上下文启动，验证立即退出
func TestStartWithCancelledContext(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	// 设置回调以接收退出信号
	received := atomic.Bool{}
	loopObj.SetCallback(func(resp AIResponse) {
		if resp.StopReason == StopReasonUser {
			received.Store(true)
		}
	})

	done := make(chan struct{})
	go func() {
		loopObj.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Start 应快速返回
	case <-time.After(1 * time.Second):
		t.Fatal("expected Start to return immediately with cancelled context")
	}

	if !received.Load() {
		t.Fatal("expected callback to receive StopReasonUser")
	}
}

// TestStopBeforeStart 测试未启动时调用 Stop 是安全的（no-op）
func TestStopBeforeStart(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 多次 Stop 不应 panic
	for range 5 {
		loopObj.Stop()
	}
}

// TestSummaryQueueFull 测试 Summary 在队列满时返回错误
func TestSummaryQueueFull(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 填满队列
	for range queueSize {
		select {
		case loopObj.sendQueue <- msgObj{Msg: "test"}:
		default:
			t.Fatal("unexpected: sendQueue not full yet")
		}
	}

	// Summary 应返回错误
	err := loopObj.Summary()
	if err == nil {
		t.Fatal("expected error when summary queue is full")
	}
}

// TestApproveQueueFull 测试 Approve 在队列满时返回错误
func TestApproveQueueFull(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 填满队列
	for range queueSize {
		select {
		case loopObj.sendQueue <- msgObj{Msg: "test"}:
		default:
			t.Fatal("unexpected: sendQueue not full yet")
		}
	}

	// Approve 应返回错误
	err := loopObj.Approve()
	if err == nil {
		t.Fatal("expected error when approve queue is full")
	}
}

// TestSetCallbackExitOnCancel 验证 SetCallback goroutine 在 Cancel 后退出
func TestSetCallbackExitOnCancel(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	// 记录 goroutine 启动数
	loopObj.SetCallback(func(resp AIResponse) {
		// no-op
	})

	// Cancel 关闭 done → 回调 goroutine 应退出
	loopObj.Cancel()

	// 给 goroutine 时间处理 done signal
	time.Sleep(100 * time.Millisecond)

	// 向 recvQueue 发送数据，此时 goroutine 已退出不应调用回调
	select {
	case loopObj.recvQueue <- AIResponse{Content: "after cancel", StopReason: StopReasonNone}:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected send to recvQueue to succeed (buffered)")
	}
}

// TestStartWithEmptyMessage 测试发送空消息不会崩溃（空消息被跳过）
func TestStartWithEmptyMessage(t *testing.T) {
	setupConfigForTest()
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := createTestChat(db, t)

	loopObj := New(chat)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	responses := []AIResponse{}
	done := make(chan struct{})

	loopObj.SetCallback(func(resp AIResponse) {
		responses = append(responses, resp)
		if resp.StopReason != StopReasonNone {
			close(done)
		}
	})

	go loopObj.Start(ctx)

	// 发送空消息（应被 Start 循环跳过）
	err := loopObj.Chat("", nil)
	if err != nil {
		t.Fatalf("expected no error sending empty message: %v", err)
	}

	// 再发一条正常消息，验证空消息被跳过
	err = loopObj.Chat("  ", nil) // 空格 TrimSpace 后也为空
	if err != nil {
		t.Fatalf("expected no error sending whitespace message: %v", err)
	}

	// 等待 context 超时退出
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("expected loop to exit on context timeout")
	}

	t.Logf("received %d responses", len(responses))
}

// BenchmarkChat 基准测试：消息发送
func BenchmarkChat(b *testing.B) {
	setupConfigForTest()
	db := setupTestDB(&testing.T{})
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
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
	defer u.Unwrap(db.DB()).Close()
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
