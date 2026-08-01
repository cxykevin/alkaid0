package request

import (
	"context"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestTitleSummary_EchoMock 测试首次标题生成：返回非空标题且不写库（写库由调用方负责）
func TestTitleSummary_EchoMock(t *testing.T) {
	// 启动 Mock 服务器
	openai.StartServerTask()

	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 100
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chatID := uint32(200)
	db.Create(&structs.Chats{ID: chatID})

	messages := []structs.Messages{
		{ChatID: chatID, Type: structs.MessagesRoleUser, Delta: "帮我实现自动标题总结"},
		{ChatID: chatID, Type: structs.MessagesRoleAgent, Delta: "好的，我来实现"},
	}
	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	ctx := context.Background()
	title, err := TitleSummary(ctx, db, chatID)
	if err != nil {
		t.Fatalf("TitleSummary failed: %v", err)
	}
	t.Logf("Generated title: %s", title)
	if title == "" {
		t.Fatal("Title should not be empty")
	}

	// 验证 DB 未被写入（写库由调用方负责）
	var chat structs.Chats
	if err := db.First(&chat, chatID).Error; err != nil {
		t.Fatalf("Failed to query chat: %v", err)
	}
	if chat.AITitle != "" {
		t.Errorf("TitleSummary should not write to DB, got AITitle=%q", chat.AITitle)
	}
}

// TestTitleSummaryFull_EchoMock 测试 compress 重生成（完整对话输入）
func TestTitleSummaryFull_EchoMock(t *testing.T) {
	// 启动 Mock 服务器
	openai.StartServerTask()

	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 100
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chatID := uint32(201)
	db.Create(&structs.Chats{ID: chatID})

	roles := []structs.MessagesRole{structs.MessagesRoleUser, structs.MessagesRoleAgent, structs.MessagesRoleUser}
	for i, role := range roles {
		msg := structs.Messages{ChatID: chatID, Type: role, Delta: "消息" + string(rune('0'+i))}
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	ctx := context.Background()
	title, err := TitleSummaryFull(ctx, db, chatID)
	if err != nil {
		t.Fatalf("TitleSummaryFull failed: %v", err)
	}
	if title == "" {
		t.Fatal("Title should not be empty")
	}
	t.Logf("Generated full title: %s", title)
}

// TestTitleSummary_NoMessages 测试空会话返回空标题
func TestTitleSummary_NoMessages(t *testing.T) {
	openai.StartServerTask()

	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 100
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	title, err := TitleSummary(context.Background(), db, 1)
	if err != nil {
		t.Fatalf("TitleSummary failed: %v", err)
	}
	if title != "" {
		t.Errorf("Expected empty title, got %q", title)
	}
}

// TestTitleSummary_ModelMissing 测试模型不可用时返回错误
func TestTitleSummary_ModelMissing(t *testing.T) {
	setupTestConfig()
	// 清空模型表，使 TitleModel=100 无法解析
	config.GlobalConfig.Model.Models = map[int32]cfgStruct.ModelConfig{}
	config.GlobalConfig.Model.DefaultModelID = 0
	config.GlobalConfig.Agent.TitleModel = 100
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	messages := []structs.Messages{
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "hi"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "hello"},
	}
	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	title, err := TitleSummary(context.Background(), db, 1)
	if err == nil {
		t.Fatal("Expected error when no model is available")
	}
	if title != "" {
		t.Errorf("Expected empty title on error, got %q", title)
	}
}
