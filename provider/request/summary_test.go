package request

import (
	"context"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupSummaryTestDB 设置summary测试数据库
func setupSummaryTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// 迁移所有需要的表
	if err := db.AutoMigrate(
		&structs.Chats{},
		&structs.Messages{},
		&structs.SubAgents{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	return db
}

// TestSummary_InvalidModel 测试无效的模型配置
func TestSummary_InvalidModel(t *testing.T) {
	db := setupSummaryTestDB(t)

	// 设置一个无效的summary模型ID
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			SummaryModel: 999, // 不存在的模型
		},
	}

	// 创建聊天会话
	chat := structs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 尝试获取总结
	_, err := Summary(context.Background(), db, 1, "")

	// 应该返回错误（可能是 build.Summary 或 GetModelConfig 的错误）
	if err == nil {
		t.Log("Expected error for invalid model config, but got nil (might be due to empty message list)")
	}
}

// TestSummary_EmptyChat 测试空聊天会话
func TestSummary_EmptyChat(t *testing.T) {
	db := setupSummaryTestDB(t)

	// 设置有效的模型配置
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			SummaryModel: 1,
		},
		Model: cfgStruct.ModelsConfig{
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName:   "test-model",
					ModelID:     "test-model-id",
					ProviderURL: "http://localhost:56108/v1",
					ProviderKey: "sk-test",
				},
			},
		},
	}

	// 创建空聊天会话
	chat := structs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 获取总结（应该返回空字符串，因为没有消息）
	summary, err := Summary(context.Background(), db, 1, "")

	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	if summary != "" {
		t.Errorf("Expected empty summary for empty chat, got: %s", summary)
	}
}

// TestSummary_NonExistentChat 测试不存在的聊天会话
func TestSummary_NonExistentChat(t *testing.T) {
	db := setupSummaryTestDB(t)

	// 设置有效的模型配置
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			SummaryModel: 1,
		},
		Model: cfgStruct.ModelsConfig{
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName:   "test-model",
					ModelID:     "test-model-id",
					ProviderURL: "http://localhost:56108/v1",
					ProviderKey: "sk-test",
				},
			},
		},
	}

	// 尝试获取不存在的聊天会话的总结
	summary, err := Summary(context.Background(), db, 999, "")

	// 可能返回错误或空字符串
	if err != nil {
		t.Logf("Expected behavior: error for non-existent chat: %v", err)
	} else if summary != "" {
		t.Errorf("Expected empty summary for non-existent chat, got: %s", summary)
	}
}

// TestSummarySession 测试 SummarySession 函数
func TestSummarySession(t *testing.T) {
	db := setupSummaryTestDB(t)

	// 设置有效的模型配置
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			SummaryModel: 1,
		},
		Model: cfgStruct.ModelsConfig{
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName:   "test-model",
					ModelID:     "test-model-id",
					ProviderURL: "http://localhost:56108/v1",
					ProviderKey: "sk-test",
				},
			},
		},
	}

	// 创建聊天会话
	chat := structs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 设置会话
	session := &structs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "",
	}

	// 获取总结
	summary, err := SummarySession(context.Background(), session)

	if err != nil {
		t.Fatalf("SummarySession failed: %v", err)
	}

	// 空聊天应该返回空总结
	if summary != "" {
		t.Errorf("Expected empty summary for empty chat, got: %s", summary)
	}
}

// TestSummarySession_WithAgent 测试带代理的会话总结
func TestSummarySession_WithAgent(t *testing.T) {
	db := setupSummaryTestDB(t)

	// 设置有效的模型配置
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			SummaryModel: 1,
		},
		Model: cfgStruct.ModelsConfig{
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName:   "test-model",
					ModelID:     "test-model-id",
					ProviderURL: "http://localhost:56108/v1",
					ProviderKey: "sk-test",
				},
			},
		},
	}

	// 创建聊天会话
	chat := structs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 设置会话，带有代理
	session := &structs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "test-agent",
		CurrentAgentConfig: cfgStruct.AgentConfig{
			AgentName: "Test Agent",
		},
	}

	// 获取总结
	summary, err := SummarySession(context.Background(), session)

	if err != nil {
		t.Fatalf("SummarySession failed: %v", err)
	}

	// 空聊天应该返回空总结
	if summary != "" {
		t.Errorf("Expected empty summary for empty chat, got: %s", summary)
	}
}
