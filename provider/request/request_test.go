package request

import (
	"context"
	"sync"
	"testing"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/library/chancall"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	testConsumerOnce sync.Once
)

// initAgentsConsumer 初始化 agents 消费者（用于测试）
func initAgentsConsumer() {
	testConsumerOnce.Do(func() {
		// 注册一个简单的 agents 消费者用于测试
		actions.Call = chancall.Register(actions.ConsumerName, func(obj any) (any, error) {
			// 只处理 Deactivate 操作以支持测试
			if deactivate, ok := obj.(actions.Deactivate); ok {
				session := deactivate.Session
				session.CurrentActivatePath = ""
				session.CurrentAgentID = ""
				session.CurrentAgentConfig = cfgStruct.AgentConfig{}
				return nil, nil
			}
			// 将其他操作的处理留空（测试中不需要）
			return nil, nil
		})
	})
}

// setupTestDB 设置测试数据库
func setupTestDB(t *testing.T) *gorm.DB {
	// 初始化 agents 消费者
	initAgentsConsumer()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	// 迁移所有需要的表
	if err := db.AutoMigrate(
		&storageStructs.Chats{},
		&storageStructs.Messages{},
		&storageStructs.SubAgents{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	return db
}

// TestUserAddMsg_Basic 测试基本的消息添加
func TestUserAddMsg_Basic(t *testing.T) {
	db := setupTestDB(t)

	// 创建一个聊天会话
	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 设置会话
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "",
	}

	// 添加消息
	err := UserAddMsg(session, "Hello, world!", nil)
	if err != nil {
		t.Fatalf("UserAddMsg failed: %v", err)
	}

	// 验证消息已添加
	var messages []storageStructs.Messages
	db.Where("chat_id = ?", 1).Find(&messages)

	if len(messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(messages))
	}

	if messages[0].Delta != "Hello, world!" {
		t.Errorf("Expected message 'Hello, world!', got '%s'", messages[0].Delta)
	}

	if messages[0].Type != storageStructs.MessagesRoleUser {
		t.Errorf("Expected message type User, got %d", messages[0].Type)
	}
}

// TestUserAddMsg_WithRefers 测试带引用的消息添加
// 注意：由于 GORM 的 gob 序列化问题，这个测试被简化
func TestUserAddMsg_WithRefers(t *testing.T) {
	// t.Skip("Skipping test due to GORM gob serialization issues with MessagesReferList")

	db := setupTestDB(t)

	// 创建一个聊天会话
	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 设置会话
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "",
	}

	// 创建引用列表
	refers := &storageStructs.MessagesReferList{
		{
			FilePath:     "/test/file.go",
			FileType:     storageStructs.MessagesReferTypeFile,
			FileFromLine: 10,
			FileToLine:   20,
			Origin:       []byte("test content"),
		},
	}

	// 添加消息
	err := UserAddMsg(session, "Check this file", refers)
	if err != nil {
		t.Fatalf("UserAddMsg failed: %v", err)
	}

	// 验证消息已添加
	var messages []storageStructs.Messages
	db.Where("chat_id = ?", 1).Find(&messages)

	if len(messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(messages))
	}

	if messages[0].Delta != "Check this file" {
		t.Errorf("Expected message 'Check this file', got '%s'", messages[0].Delta)
	}

	if len(messages[0].Refers) != 1 {
		t.Errorf("Expected 1 refer, got %d", len(messages[0].Refers))
	}

	if messages[0].Refers[0].FilePath != "/test/file.go" {
		t.Errorf("Expected file path '/test/file.go', got '%s'", messages[0].Refers[0].FilePath)
	}
}

// TestUserAddMsg_NilRefers 测试 nil 引用
func TestUserAddMsg_NilRefers(t *testing.T) {
	db := setupTestDB(t)

	// 创建一个聊天会话
	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 设置会话
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "",
	}

	// 添加消息，传入 nil refers
	err := UserAddMsg(session, "Message without refers", nil)
	if err != nil {
		t.Fatalf("UserAddMsg failed: %v", err)
	}

	// 验证消息已添加
	var messages []storageStructs.Messages
	db.Where("chat_id = ?", 1).Find(&messages)

	if len(messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(messages))
	}

	if len(messages[0].Refers) != 0 {
		t.Errorf("Expected 0 refers, got %d", len(messages[0].Refers))
	}
}

// TestUserAddMsg_MultipleMessages 测试添加多条消息
func TestUserAddMsg_MultipleMessages(t *testing.T) {
	db := setupTestDB(t)

	// 创建一个聊天会话
	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 设置会话
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "",
	}

	// 添加多条消息
	messages := []string{"First message", "Second message", "Third message"}
	for _, msg := range messages {
		err := UserAddMsg(session, msg, nil)
		if err != nil {
			t.Fatalf("UserAddMsg failed: %v", err)
		}
	}

	// 验证所有消息已添加
	var dbMessages []storageStructs.Messages
	db.Where("chat_id = ?", 1).Order("id ASC").Find(&dbMessages)

	if len(dbMessages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(dbMessages))
	}

	for i, msg := range messages {
		if dbMessages[i].Delta != msg {
			t.Errorf("Message %d: expected '%s', got '%s'", i, msg, dbMessages[i].Delta)
		}
	}
}

// TestUserAddMsg_EmptyMessage 测试空消息
func TestUserAddMsg_EmptyMessage(t *testing.T) {
	db := setupTestDB(t)

	// 创建一个聊天会话
	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 设置会话
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "",
	}

	// 添加空消息
	err := UserAddMsg(session, "", nil)
	if err != nil {
		t.Fatalf("UserAddMsg failed: %v", err)
	}

	// 验证消息已添加
	var messages []storageStructs.Messages
	db.Where("chat_id = ?", 1).Find(&messages)

	if len(messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(messages))
	}

	if messages[0].Delta != "" {
		t.Errorf("Expected empty message, got '%s'", messages[0].Delta)
	}
}

// TestUserAddMsg_InvalidChatID 测试无效的聊天ID
func TestUserAddMsg_InvalidChatID(t *testing.T) {
	db := setupTestDB(t)

	// 不创建聊天会话，直接使用不存在的ID
	session := &storageStructs.Chats{
		ID:             999, // 不存在的ID
		DB:             db,
		CurrentAgentID: "",
	}

	// 尝试添加消息（由于外键约束，这应该会失败）
	// 但是 GORM 默认不强制外键约束在 SQLite 中
	// 所以这个测试可能会成功，取决于数据库配置
	err := UserAddMsg(session, "Test message", nil)

	// SQLite 默认不强制外键，所以这可能不会失败
	// 我们只是验证函数能够处理这种情况
	if err != nil {
		t.Logf("Expected behavior: error when chat doesn't exist: %v", err)
	}
}

// TestUserAddMsg_WithCurrentAgent 测试当有当前代理时的行为
func TestUserAddMsg_WithCurrentAgent(t *testing.T) {

	db := setupTestDB(t)

	// 创建一个聊天会话
	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 创建一个子代理
	subAgent := storageStructs.SubAgents{
		ID:       "test-agent",
		AgentID:  "test-agent-id",
		BindPath: "/test/path",
		Deleted:  false,
	}
	if err := db.Create(&subAgent).Error; err != nil {
		t.Fatalf("Failed to create sub agent: %v", err)
	}

	// 设置会话，带有当前代理
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "test-agent",
		CurrentAgentConfig: cfgStruct.AgentConfig{
			AgentName: "Test Agent",
		},
	}

	// DeactivateAgent 将通过 chancall 调用
	err := UserAddMsg(session, "Message with agent", nil)

	if err != nil {
		t.Fatalf("UserAddMsg failed: %v", err)
	}

	// 验证消息已添加
	var messages []storageStructs.Messages
	db.Where("chat_id = ?", 1).Find(&messages)

	if len(messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(messages))
	}
}

// TestCanAutoApprove 测试自动审批和拒绝逻辑
func TestCanAutoApprove(t *testing.T) {
	db := setupTestDB(t)

	// 设置基础环境
	session := &storageStructs.Chats{
		ID: 1,
		DB: db,
		CurrentAgentConfig: cfgStruct.AgentConfig{
			AutoApprove: "ToolCall.Name == \"approve_me\"",
			AutoReject:  "ToolCall.Name == \"reject_me\"",
		},
	}
	msg := &storageStructs.Messages{}

	// 1. 测试拒绝规则触发
	callsReject := []ToolCall{
		{Name: "reject_me", ID: "1"},
	}
	approved, reason, err := CanAutoApprove(session, callsReject, msg)
	if err != nil {
		t.Fatalf("CanAutoApprove failed: %v", err)
	}
	if approved {
		t.Error("Expected rejected for 'reject_me', but got approved")
	}
	_ = reason

	// 2. 测试审批规则触发
	callsApprove := []ToolCall{
		{Name: "approve_me", ID: "2"},
	}
	approved, reason, err = CanAutoApprove(session, callsApprove, msg)
	if err != nil {
		t.Fatalf("CanAutoApprove failed: %v", err)
	}
	if !approved {
		t.Error("Expected approved for 'approve_me', but got rejected")
	}

	// 3. 测试混合调用（一个审批，一个拒绝）-> 应该拒绝
	callsMixed := []ToolCall{
		{Name: "approve_me", ID: "3"},
		{Name: "reject_me", ID: "4"},
	}
	approved, reason, err = CanAutoApprove(session, callsMixed, msg)
	if err != nil {
		t.Fatalf("CanAutoApprove failed: %v", err)
	}
	if approved {
		t.Error("Expected rejected for mixed calls containing 'reject_me', but got approved")
	}

	// 4. 测试未命中任何规则 -> 应该拒绝
	callsNone := []ToolCall{
		{Name: "unknown_tool", ID: "5"},
	}
	approved, reason, err = CanAutoApprove(session, callsNone, msg)
	if err != nil {
		t.Fatalf("CanAutoApprove failed: %v", err)
	}
	if approved {
		t.Error("Expected rejected for unknown tool, but got approved")
	}

	// 5. 测试参数检查 (hasParam, param)
	session.CurrentAgentConfig.AutoApprove = "hasParam(ToolCall, \"safe\") && param(ToolCall, \"safe\") == true"
	safeVal := any(true)
	callsParam := []ToolCall{
		{
			Name: "any_tool",
			ID:   "6",
			Parameters: map[string]*any{
				"safe": &safeVal,
			},
		},
	}
	approved, reason, err = CanAutoApprove(session, callsParam, msg)
	if err != nil {
		t.Fatalf("CanAutoApprove failed with params: %v", err)
	}
	if !approved {
		t.Error("Expected approved for tool with safe=true parameter")
	}
}

// TestStringDefault 测试 stringDefault 辅助函数
func TestStringDefault(t *testing.T) {
	// 测试 nil 指针
	if result := stringDefault(nil); result != "" {
		t.Errorf("Expected empty string for nil, got '%s'", result)
	}

	// 测试非 nil 指针
	str := "test string"
	if result := stringDefault(&str); result != "test string" {
		t.Errorf("Expected 'test string', got '%s'", result)
	}

	// 测试空字符串指针
	emptyStr := ""
	if result := stringDefault(&emptyStr); result != "" {
		t.Errorf("Expected empty string, got '%s'", result)
	}
}

// TestSendRequest_ModelNotFound 测试模型不存在的情况
func TestSendRequest_ModelNotFound(t *testing.T) {
	db := setupTestDB(t)

	// 创建聊天会话
	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 999, // 不存在的模型ID
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 设置会话
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		LastModelID:    999,
		CurrentAgentID: "",
	}

	// 尝试发送请求
	_, err := SendRequest(context.Background(), session, func(delta, thinking string, _ uint64, _ structs.Usage) error {
		return nil
	})

	// 应该返回 "model not found" 错误
	if err == nil {
		t.Fatal("Expected error for non-existent model, got nil")
	}

	if err.Error() != "model not found" {
		t.Errorf("Expected 'model not found' error, got: %v", err)
	}
}
