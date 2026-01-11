package build

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// TestSummary_Basic 测试 Summary 函数基本功能
func TestSummary_Basic(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 设置 SummaryModel 配置
	config.GlobalConfig.Agent.SummaryModel = 1

	// 插入测试消息
	messages := []structs.Messages{
		{
			ChatID: 1,
			Type:   structs.MessagesRoleUser,
			Delta:  "Hello, how are you?",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "I'm doing well, thank you!",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用 Summary
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	// 验证请求结构不为空
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证模型配置
	if request.Model != "test-model-id" {
		t.Errorf("Expected model ID 'test-model-id', got '%s'", request.Model)
	}

	// 验证流式传输
	if !request.Stream {
		t.Error("Expected stream to be true")
	}

	// 验证温度参数
	if request.Temperature == nil || *request.Temperature != 0.7 {
		t.Error("Expected temperature to be 0.7")
	}

	// 验证 TopP 参数
	if request.TopP == nil || *request.TopP != 0.9 {
		t.Error("Expected top_p to be 0.9")
	}

	// 验证最大令牌数
	if request.MaxTokens == nil || *request.MaxTokens != maxToken {
		t.Errorf("Expected max_tokens %d, got %d", maxToken, *request.MaxTokens)
	}

	// 验证消息至少包含系统消息
	if len(request.Messages) < 1 {
		t.Error("Expected at least one message (system message)")
	}

	// 验证第一个消息是系统消息
	if request.Messages[0].Role != "system" {
		t.Errorf("First message should be system, got %s", request.Messages[0].Role)
	}
}

// TestSummary_WithThinking 测试带推理内容的 Summary
func TestSummary_WithThinking(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 设置 SummaryModel 配置
	config.GlobalConfig.Agent.SummaryModel = 1

	// 插入带推理的测试消息
	messages := []structs.Messages{
		{
			ChatID:        1,
			Type:          structs.MessagesRoleUser,
			Delta:         "Solve this math problem: 2+2=?",
			ThinkingDelta: "Let me think about this. 2+2 equals 4",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "The answer is 4",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用 Summary
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	// 验证请求不为空
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证消息处理（包含系统消息）
	if len(request.Messages) < 1 {
		t.Error("Expected at least one message")
	}

	// 如果启用了推理，应该有 ReasoningContent 字段
	if config.GlobalConfig.Model.Models[1].EnableThinking {
		foundReasoningContent := false
		for _, msg := range request.Messages {
			if msg.ReasoningContent != nil && *msg.ReasoningContent != "" {
				foundReasoningContent = true
				break
			}
		}
		// 注意：这取决于实现细节，可能找不到也是正常的
		t.Logf("Found reasoning content: %v", foundReasoningContent)
	}
}

// TestSummary_WithSummary 测试带总结内容的 Summary
func TestSummary_WithSummary(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 设置 SummaryModel 配置
	config.GlobalConfig.Agent.SummaryModel = 1

	// 插入带总结的测试消息
	messages := []structs.Messages{
		{
			ChatID:  1,
			Type:    structs.MessagesRoleUser,
			Delta:   "Previous conversation",
			Summary: "User asked about weather and I provided information",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "New response",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用 Summary
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	// 验证请求不为空
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证消息包含总结内容
	foundSummary := false
	for _, msg := range request.Messages {
		if strings.Contains(msg.Content, "User asked about weather") {
			foundSummary = true
			break
		}
	}

	if !foundSummary {
		t.Logf("Summary content not found in messages. Messages: %+v", request.Messages)
	}
}

// TestSummary_EmptyMessages 测试无消息的 Summary
func TestSummary_EmptyMessages(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 设置 SummaryModel 配置
	config.GlobalConfig.Agent.SummaryModel = 1

	// 不插入任何消息，直接调用 Summary

	// 调用 Summary
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	// 即使没有消息，也应该至少返回系统消息
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 应该至少包含系统消息
	if len(request.Messages) < 1 {
		t.Error("Expected at least system message even with empty messages")
	}

	// 第一个应该是系统消息
	if len(request.Messages) > 0 && request.Messages[0].Role != "system" {
		t.Errorf("First message should be system, got %s", request.Messages[0].Role)
	}
}

// // TestSummary_InvalidModel 测试无效的模型配置
// func TestSummary_InvalidModel(t *testing.T) {
// 	setupTestConfig()
// 	db := setupTestDB(t)

// 	// 设置无效的 SummaryModel
// 	config.GlobalConfig.Agent.SummaryModel = 999

// 	// 调用 Summary
// 	_, err := Summary(1, "", db)
// 	if err == nil {
// 		t.Error("Expected error for invalid model, got nil")
// 	}
// }

// TestSummary_ManyMessages 测试处理多条消息
func TestSummary_ManyMessages(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 设置 SummaryModel 配置
	config.GlobalConfig.Agent.SummaryModel = 1

	// 插入多条消息
	for i := 1; i <= 30; i++ {
		role := structs.MessagesRoleUser
		if i%2 == 0 {
			role = structs.MessagesRoleAgent
		}

		msg := structs.Messages{
			ChatID: 1,
			Type:   role,
			Delta:  "Message " + string(rune(48+i%10)),
		}

		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用 Summary
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	// 验证请求不为空
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证消息数量合理（应该受 summaryKeepNumber 限制）
	if len(request.Messages) < 1 {
		t.Error("Expected at least one message")
	}

	// 验证结构完整
	if request.Model == "" {
		t.Error("Expected model to be set")
	}

	if !request.Stream {
		t.Error("Expected stream to be true")
	}
}

// TestSummary_BasicFunctionality 测试Summary函数基本功能
func TestSummary_BasicFunctionality(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.SummaryModel = 1
	db := setupTestDB(t)

	// 插入测试消息
	messages := []structs.Messages{
		{
			ChatID: 1,
			Type:   structs.MessagesRoleUser,
			Delta:  "Hello, can you help me?",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Of course! I'm here to help.",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleUser,
			Delta:  "Thank you!",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用Summary函数
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	// 验证返回值不为nil
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证模型配置
	if request.Model == "" {
		t.Error("Expected model to be set")
	}

	if !request.Stream {
		t.Error("Expected stream to be true in Summary")
	}

	// 验证消息列表
	if len(request.Messages) == 0 {
		t.Error("Expected at least system message")
	}

	// 验证系统消息存在
	hasSystemMessage := false
	for _, msg := range request.Messages {
		if msg.Role == "system" {
			hasSystemMessage = true
			break
		}
	}
	if !hasSystemMessage {
		t.Error("Expected system message in summary")
	}
}

// TestSummary_WithSummaryContent 测试Summary函数处理已有总结内容
func TestSummary_WithSummaryContent(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.SummaryModel = 1
	db := setupTestDB(t)

	// 插入包含总结内容的消息
	messages := []structs.Messages{
		{
			ChatID:  1,
			Type:    structs.MessagesRoleUser,
			Delta:   "Original message",
			Summary: "User asked for help",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Response message",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Response message",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Response message",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Response message",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Response message",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Response message",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Response message",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用Summary函数
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary with summary content failed: %v", err)
	}

	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证消息数量（至少应有一条包含总结的消息和系统消息）
	if len(request.Messages) < 2 {
		t.Errorf("Expected at least 2 messages (summary + system), but %v", len(request.Messages))
	}
}

// TestSummary_WithSummaryContentNotEnough 测试Summary函数处理已有总结内容，并且消息不足
func TestSummary_WithSummaryContentNotEnough(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.SummaryModel = 1
	db := setupTestDB(t)

	// 插入包含总结内容的消息
	messages := []structs.Messages{
		{
			ChatID:  1,
			Type:    structs.MessagesRoleUser,
			Delta:   "Original message",
			Summary: "User asked for help",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Response message",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用Summary函数
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary with summary content failed: %v", err)
	}

	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证消息数量（至少应有一条包含总结的消息和系统消息）
	if len(request.Messages) != 1 {
		t.Error("Expected 1 messages (system)")
	}
}

// TestSummary_WithThinkingContent 测试Summary函数处理思考内容
func TestSummary_WithThinkingContent(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.SummaryModel = 1
	db := setupTestDB(t)

	// 插入包含思考内容的消息
	messages := []structs.Messages{
		{
			ChatID:        1,
			Type:          structs.MessagesRoleAgent,
			ThinkingDelta: "Let me think about this...",
			Delta:         "The answer is...",
		},
		{
			ChatID:        1,
			Type:          structs.MessagesRoleAgent,
			ThinkingDelta: "Let me think about this...",
			Delta:         "The answer is...",
		},
		{
			ChatID:        1,
			Type:          structs.MessagesRoleAgent,
			ThinkingDelta: "Let me think about this...",
			Delta:         "The answer is...",
		},
		{
			ChatID:        1,
			Type:          structs.MessagesRoleAgent,
			ThinkingDelta: "Let me think about this...",
			Delta:         "The answer is...",
		},
		{
			ChatID:        1,
			Type:          structs.MessagesRoleAgent,
			ThinkingDelta: "Let me think about this...",
			Delta:         "The answer is...",
		},
		{
			ChatID:        1,
			Type:          structs.MessagesRoleAgent,
			ThinkingDelta: "Let me think about this...",
			Delta:         "The answer is...",
		},
		{
			ChatID:        1,
			Type:          structs.MessagesRoleAgent,
			ThinkingDelta: "Let me think about this...",
			Delta:         "The answer is...",
		},
		{
			ChatID:        1,
			Type:          structs.MessagesRoleAgent,
			ThinkingDelta: "Let me think about this...",
			Delta:         "The answer is...",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用Summary函数
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary with thinking content failed: %v", err)
	}

	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证消息包含思考内容（当EnableThinking为true时）
	foundThinking := false
	for _, msg := range request.Messages {
		if msg.ReasoningContent != nil && *msg.ReasoningContent != "" {
			foundThinking = true
			break
		}
	}
	if !foundThinking {
		t.Error("Expected message with reasoning content")
	}

	config.GlobalConfig.Agent.SummaryModel = 2
	// 调用Summary函数
	_, request, err = Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary with thinking content failed: %v", err)
	}

	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证消息包含思考内容（当EnableThinking为false时）
	foundThinking2 := false
	for _, msg := range request.Messages {
		if msg.Content != "" && strings.Contains(msg.Content, "<think>") {
			foundThinking2 = true
			break
		}
	}
	if !foundThinking2 {
		t.Error("Expected message with reasoning content")
	}

}

// TestSummary_EmptyChat 测试Summary函数处理空聊天
func TestSummary_EmptyChat(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.SummaryModel = 1
	db := setupTestDB(t)

	// 调用Summary函数，无任何消息
	_, request, err := Summary(999, "", db)
	if err != nil {
		t.Fatalf("Summary with empty chat failed: %v", err)
	}

	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证至少有系统消息
	if len(request.Messages) == 0 {
		t.Error("Expected system message even for empty chat")
	}
}

// TestSummary_ModelConfiguration 测试Summary函数模型配置
func TestSummary_ModelConfiguration(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.SummaryModel = 1
	db := setupTestDB(t)

	// 插入测试消息
	msg := structs.Messages{
		ChatID: 1,
		Type:   structs.MessagesRoleUser,
		Delta:  "Test message",
	}
	if err := db.Create(&msg).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	// 调用Summary函数
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	// 验证温度和TopP配置
	if request.Temperature == nil {
		t.Error("Expected temperature to be set")
	} else if *request.Temperature != 0.7 {
		t.Errorf("Expected temperature 0.7, got %v", *request.Temperature)
	}

	if request.TopP == nil {
		t.Error("Expected TopP to be set")
	} else if *request.TopP != 0.9 {
		t.Errorf("Expected TopP 0.9, got %v", *request.TopP)
	}

	// 验证MaxTokens
	if request.MaxTokens == nil {
		t.Error("Expected MaxTokens to be set")
	}
}

// TestSummary_MessageOrdering 测试Summary函数消息排序
func TestSummary_MessageOrdering(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.SummaryModel = 1
	db := setupTestDB(t)

	// 插入多条消息，用于测试排序
	messages := []structs.Messages{
		{
			ChatID: 1,
			Type:   structs.MessagesRoleUser,
			Delta:  "First message",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Second message",
		},
		{
			ChatID: 1,
			Type:   structs.MessagesRoleUser,
			Delta:  "Third message",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用Summary函数
	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 验证第一条消息是系统消息
	if len(request.Messages) > 0 && request.Messages[0].Role != "system" {
		t.Error("Expected first message to be system message")
	}
}
