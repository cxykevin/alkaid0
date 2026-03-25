package request

import (
	"context"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/storage/structs"
)

func setupTestConfig() {
	config.GlobalConfig.Model.Models = make(map[int32]cfgStruct.ModelConfig)
	config.GlobalConfig.Agent.SummaryModel = 100
	config.GlobalConfig.Model.Models[100] = cfgStruct.ModelConfig{
		ModelID:     "echo-chat-flash",
		ProviderURL: "http://localhost:56108/v1",
		ProviderKey: "mock-key",
		ModelName:   "Echo Mock Model",
	}
}

func TestSummary_EchoMock(t *testing.T) {
	// 启动 Mock 服务器
	openai.StartServerTask()

	setupTestConfig()
	db := setupTestDB(t)

	// 插入测试消息
	chatID := uint32(100)
	testContent := "This is a test message for summary."

	// 创建 Chat
	db.Create(&structs.Chats{ID: chatID})

	messages := []structs.Messages{
		{
			ChatID: chatID,
			Type:   structs.MessagesRoleUser,
			Delta:  testContent,
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	// 调用 Summary
	ctx := context.Background()
	summary, err := Summary(ctx, db, chatID, "")
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	t.Logf("Generated summary: %s", summary)

	if summary == "" {
		t.Fatal("Summary should not be empty")
	}

	// 验证数据库中是否已更新
	var savedMsg structs.Messages
	err = db.Where("chat_id = ?", chatID).Order("id DESC").First(&savedMsg).Error
	if err != nil {
		t.Fatalf("Failed to query saved message: %v", err)
	}

	if savedMsg.Summary != summary {
		t.Errorf("DB summary mismatch. Expected %s, got %s", summary, savedMsg.Summary)
	}
}

func TestSummary_EchoMock_WithKeepNum(t *testing.T) {
	// 启动 Mock 服务器
	openai.StartServerTask()

	setupTestConfig()
	db := setupTestDB(t)

	// 插入多条消息，触发 keepNum 逻辑
	chatID := uint32(101)
	db.Create(&structs.Chats{ID: chatID})

	for i := 1; i <= 10; i++ {
		msg := structs.Messages{
			ChatID: chatID,
			Type:   structs.MessagesRoleUser,
			Delta:  strings.Repeat("A", i),
		}
		db.Create(&msg)
	}

	// 调用 Summary
	ctx := context.Background()
	summary, err := Summary(ctx, db, chatID, "")
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	t.Logf("Generated summary for many messages: %s", summary)

	if summary == "" {
		t.Fatal("Summary should not be empty")
	}
}
