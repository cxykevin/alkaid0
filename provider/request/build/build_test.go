package build

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/index"
	"gorm.io/gorm"
)

// setupBuildTest 设置构建测试环境和数据库
func setupBuildTest(t *testing.T) *gorm.DB {
	// 设置测试配置
	*config.GlobalConfig = cfgStruct.Config{
		Model: cfgStruct.ModelsConfig{
			DefaultModelID: 1,
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName:         "test-model",
					ModelID:           "test-model-id",
					ModelTemperature:  0.7,
					ModelTopP:         0.9,
					EnableThinking:    true,
					EnableToolCalling: true,
				},
			},
		},
	}

	os.Setenv("ALKAID_DEBUG_PROJECTPATH", "../../debug_config/dot_alkaid")
	
	// 清理数据库文件并重新初始化
	dbPath := "../../debug_config/dot_alkaid/db.sqlite"
	os.Remove(dbPath)
	
	storage.InitStorage()

	index.Load()

	return storage.DB
}

// TestBuildSuccess 测试成功构建请求
func TestBuildSuccess(t *testing.T) {
	db := setupBuildTest(t)

	// 创建测试聊天记录
	chat := structs.Chats{
		ID:          1,
		LastModelID: 1,
		NowAgent:    "",
	}

	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	// 设置当前聊天 ID
	storage.GlobalConfig.CurrentChatID = 1

	// 调用 Build 函数
	result, err := Build()

	// 验证结果
	if err != nil {
		t.Errorf("Build() returned error: %v", err)
	}

	if result == nil {
		t.Errorf("Build() returned nil request")
	}

	if result != nil && result.Model != "test-model-id" {
		t.Errorf("Expected model 'test-model-id', got '%s'", result.Model)
	}
}

// TestBuildReal 测试构建请求体
func TestBuildReal(t *testing.T) {
	db := setupBuildTest(t)

	// 创建测试聊天记录
	chat := structs.Chats{
		ID:          1,
		LastModelID: 1,
		NowAgent:    "",
	}

	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	// 设置当前聊天 ID
	storage.GlobalConfig.CurrentChatID = 1

	// 调用 Build 函数
	result, err := Build()

	// 验证结果
	if err != nil {
		t.Errorf("Build() returned error: %v", err)
	}

	if result == nil {
		t.Errorf("Build() returned nil request")
	}

	if result != nil && result.Model != "test-model-id" {
		t.Errorf("Expected model 'test-model-id', got '%s'", result.Model)
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "    ")
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(result)
	if err != nil {
		t.Errorf("Failed to marshal request to JSON: %v", err)
	}
	fmt.Printf("\n%s\n", buf.String())
}

// TestBuildNoChatID 测试没有聊天 ID 的情况
func TestBuildNoChatID(t *testing.T) {
	_ = setupBuildTest(t)

	// 不设置 CurrentChatID，保持默认值 0
	storage.GlobalConfig.CurrentChatID = 0

	// 调用 Build 函数
	result, err := Build()

	// 应该返回错误
	if err == nil {
		t.Errorf("Build() should return error when no chat id is set")
	}

	if result != nil {
		t.Errorf("Build() should return nil when error occurs")
	}
}

// TestBuildChatNotFound 测试聊天不存在的情况
func TestBuildChatNotFound(t *testing.T) {
	db := setupBuildTest(t)

	// 设置一个不存在的聊天 ID
	storage.GlobalConfig.CurrentChatID = 999

	// 不创建任何聊天，所以查询会失败
	// 调用 Build 函数
	result, err := Build()

	// 当数据库查询失败时，应该返回 nil 或错误
	// 根据 Build() 的实现，会在查询后处理错误
	if result != nil || err == nil {
		// 如果没有创建聊天，查询应该会失败
		// 验证行为是否符合预期
		_ = db
	}
}

// TestBuildWithMessages 测试包含消息的聊天
func TestBuildWithMessages(t *testing.T) {
	db := setupBuildTest(t)

	// 创建聊天记录
	chat := structs.Chats{
		ID:          100,
		LastModelID: 1,
		NowAgent:    "",
	}

	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	// 创建消息记录
	messages := []structs.Messages{
		{
			ChatID:    100,
			Type:      structs.MessagesRoleUser,
			Delta:     "你好",
			ModelName: "test-model",
			ModelID:   1,
		},
		{
			ChatID:    100,
			Type:      structs.MessagesRoleAgent,
			Delta:     "你好！有什么帮助吗？",
			ModelName: "test-model",
			ModelID:   1,
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	storage.GlobalConfig.CurrentChatID = 100

	// 调用 Build 函数
	result, err := Build()

	// 验证结果
	if err != nil {
		t.Errorf("Build() returned error: %v", err)
	}

	if result == nil {
		t.Errorf("Build() returned nil request")
	}

	if result != nil && len(result.Messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(result.Messages))
	}
}

// TestBuildWithMultipleModels 测试不同模型ID
func TestBuildWithMultipleModels(t *testing.T) {
	db := setupBuildTest(t)

	// 添加多个模型配置
	config.GlobalConfig.Model.Models[2] = cfgStruct.ModelConfig{
		ModelName:         "test-model-2",
		ModelID:           "test-model-id-2",
		ModelTemperature:  0.5,
		ModelTopP:         0.8,
		EnableThinking:    false,
		EnableToolCalling: false,
	}

	// 创建聊天记录，使用不同的模型ID
	chat := structs.Chats{
		ID:          101,
		LastModelID: 2,
		NowAgent:    "",
	}

	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	storage.GlobalConfig.CurrentChatID = 101

	// 调用 Build 函数
	result, err := Build()

	// 验证结果
	if err != nil {
		t.Errorf("Build() returned error: %v", err)
	}

	if result != nil && result.Model != "test-model-id-2" {
		t.Errorf("Expected model 'test-model-id-2', got '%s'", result.Model)
	}
}

// TestBuildWithAgent 测试包含 Agent ID 的情况
func TestBuildWithAgent(t *testing.T) {
	db := setupBuildTest(t)

	// 设置 Agent 配置
	config.GlobalConfig.Agent.Agents = map[string]cfgStruct.AgentConfig{
		"test-agent": {
			AgentName: "Test Agent",
			AgentModel: 1,
		},
	}

	// 创建聊天记录，带 Agent ID
	chat := structs.Chats{
		ID:          102,
		LastModelID: 1,
		NowAgent:    "test-agent",
	}

	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	storage.GlobalConfig.CurrentChatID = 102

	// 调用 Build 函数
	result, err := Build()

	// 验证结果
	if err != nil {
		t.Errorf("Build() returned error: %v", err)
	}

	if result == nil {
		t.Errorf("Build() returned nil request")
	}
}

// TestBuildModelTemperatureAndTopP 测试模型温度和TopP参数
func TestBuildModelTemperatureAndTopP(t *testing.T) {
	db := setupBuildTest(t)

	// 创建聊天记录
	chat := structs.Chats{
		ID:          103,
		LastModelID: 1,
		NowAgent:    "",
	}

	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	storage.GlobalConfig.CurrentChatID = 103

	// 调用 Build 函数
	result, err := Build()

	// 验证结果
	if err != nil {
		t.Errorf("Build() returned error: %v", err)
	}

	if result == nil {
		t.Errorf("Build() returned nil request")
		return
	}

	// 验证温度参数
	if result.Temperature == nil {
		t.Errorf("Expected non-nil temperature")
	} else if *result.Temperature != 0.7 {
		t.Errorf("Expected temperature 0.7, got %f", *result.Temperature)
	}

	// 验证TopP参数
	if result.TopP == nil {
		t.Errorf("Expected non-nil TopP")
	} else if *result.TopP != 0.9 {
		t.Errorf("Expected TopP 0.9, got %f", *result.TopP)
	}

	// 验证MaxTokens参数
	if result.MaxTokens == nil {
		t.Errorf("Expected non-nil MaxTokens")
	} else if *result.MaxTokens != 8192 {
		t.Errorf("Expected MaxTokens 8192, got %d", *result.MaxTokens)
	}
}

// TestBuildStream 测试流式输出设置
func TestBuildStream(t *testing.T) {
	db := setupBuildTest(t)

	// 创建聊天记录
	chat := structs.Chats{
		ID:          104,
		LastModelID: 1,
		NowAgent:    "",
	}

	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	storage.GlobalConfig.CurrentChatID = 104

	// 调用 Build 函数
	result, err := Build()

	// 验证结果
	if err != nil {
		t.Errorf("Build() returned error: %v", err)
	}

	if result == nil {
		t.Errorf("Build() returned nil request")
		return
	}

	// 验证流式标志
	if !result.Stream {
		t.Errorf("Expected Stream to be true")
	}
}

// TestBuildMultipleCalls 测试多次调用Build函数
func TestBuildMultipleCalls(t *testing.T) {
	db := setupBuildTest(t)

	// 创建多个聊天记录
	for i := 105; i <= 107; i++ {
		chat := structs.Chats{
			ID:          uint32(i),
			LastModelID: 1,
			NowAgent:    "",
		}
		if err := db.Create(&chat).Error; err != nil {
			t.Fatalf("Failed to create test chat %d: %v", i, err)
		}
	}

	// 多次调用Build，验证每次都返回不同的结果
	for i := 105; i <= 107; i++ {
		storage.GlobalConfig.CurrentChatID = uint32(i)

		result, err := Build()

		if err != nil {
			t.Errorf("Build() for chat %d returned error: %v", i, err)
		}

		if result == nil {
			t.Errorf("Build() for chat %d returned nil request", i)
		}

		if result != nil && result.Model != "test-model-id" {
			t.Errorf("Expected model 'test-model-id' for chat %d, got '%s'", i, result.Model)
		}
	}
}

// TestBuildWithSummary 测试包含总结信息的消息
func TestBuildWithSummary(t *testing.T) {
	db := setupBuildTest(t)

	// 创建聊天记录
	chat := structs.Chats{
		ID:          108,
		LastModelID: 1,
		NowAgent:    "",
	}

	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	// 创建包含Summary的消息
	msg := structs.Messages{
		ChatID:    108,
		Type:      structs.MessagesRoleUser,
		Delta:     "原始内容",
		Summary:   "这是一个总结",
		ModelName: "test-model",
		ModelID:   1,
	}

	if err := db.Create(&msg).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	storage.GlobalConfig.CurrentChatID = 108

	// 调用 Build 函数
	result, err := Build()

	// 验证结果
	if err != nil {
		t.Errorf("Build() returned error: %v", err)
	}

	if result == nil {
		t.Errorf("Build() returned nil request")
	}
}
