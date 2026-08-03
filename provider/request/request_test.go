package request

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/library/chancall"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
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
	defer u.Unwrap(db.DB()).Close()

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
	defer u.Unwrap(db.DB()).Close()

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
	defer u.Unwrap(db.DB()).Close()

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
	defer u.Unwrap(db.DB()).Close()

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
	defer u.Unwrap(db.DB()).Close()

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
	defer u.Unwrap(db.DB()).Close()

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
	defer u.Unwrap(db.DB()).Close()

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
	defer u.Unwrap(db.DB()).Close()

	// 设置基础环境
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "test-agent",
		NowAgent:       "test-agent",
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
	defer u.Unwrap(db.DB()).Close()

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
	_, err := SendRequest(context.Background(), session, func(delta, thinking string, _ uint64, _ structs.Usage, _ *string) error {
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

// TestEvaluateApprovalRules_AgentLevel 测试 agent 级别规则
func TestEvaluateApprovalRules_AgentLevel(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "test-agent",
		NowAgent:       "test-agent",
		CurrentAgentConfig: cfgStruct.AgentConfig{
			AutoApprove: "ToolCall.Name == \"approve_me\"",
			AutoReject:  "ToolCall.Name == \"reject_me\"",
		},
	}

	// 1. 拒绝规则触发
	result, _ := EvaluateApprovalRules(session, []ToolCall{{Name: "reject_me", ID: "1"}})
	if result.Decision != DecisionRejected {
		t.Errorf("Expected DecisionRejected, got %v", result.Decision)
	}
	if result.Reason == "" {
		t.Error("Expected non-empty reason for reject")
	}

	// 2. 审批规则触发
	result, _ = EvaluateApprovalRules(session, []ToolCall{{Name: "approve_me", ID: "2"}})
	if result.Decision != DecisionApproved {
		t.Errorf("Expected DecisionApproved, got %v", result.Decision)
	}

	// 3. 混合调用 — 拒绝优先
	result, _ = EvaluateApprovalRules(session, []ToolCall{
		{Name: "approve_me", ID: "3"},
		{Name: "reject_me", ID: "4"},
	})
	if result.Decision != DecisionRejected {
		t.Errorf("Expected DecisionRejected for mixed calls, got %v", result.Decision)
	}

	// 4. 无规则命中
	result, _ = EvaluateApprovalRules(session, []ToolCall{{Name: "unknown_tool", ID: "5"}})
	if result.Decision != DecisionManual {
		t.Errorf("Expected DecisionManual for unknown tool, got %v", result.Decision)
	}
}

// TestEvaluateApprovalRules_EmptyConfig 测试零值配置（无 AutoApprove/AutoReject）
func TestEvaluateApprovalRules_EmptyConfig(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	session := &storageStructs.Chats{
		ID:                 1,
		DB:                 db,
		CurrentAgentConfig: cfgStruct.AgentConfig{}, // 零值
	}

	// 无任何规则 → DecisionManual
	result, _ := EvaluateApprovalRules(session, []ToolCall{{Name: "edit", ID: "1"}})
	if result.Decision != DecisionManual {
		t.Errorf("Expected DecisionManual with empty config, got %v", result.Decision)
	}
}

// TestEvaluateApprovalRules_GlobalDefaults 测试全局默认值生效
func TestEvaluateApprovalRules_GlobalDefaults(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	// 保存原始配置
	oldCfg := config.GlobalConfig.Agent.DefaultAutoApprove
	oldReject := config.GlobalConfig.Agent.DefaultAutoReject
	oldIgnore := config.GlobalConfig.Agent.IgnoreDefaultRules
	defer func() {
		config.GlobalConfig.Agent.DefaultAutoApprove = oldCfg
		config.GlobalConfig.Agent.DefaultAutoReject = oldReject
		config.GlobalConfig.Agent.IgnoreDefaultRules = oldIgnore
	}()

	config.GlobalConfig.Agent.DefaultAutoApprove = "true"
	config.GlobalConfig.Agent.DefaultAutoReject = ""
	config.GlobalConfig.Agent.IgnoreDefaultRules = true

	session := &storageStructs.Chats{
		ID:                 1,
		DB:                 db,
		CurrentAgentConfig: cfgStruct.AgentConfig{}, // 零值，应回退到全局默认
	}

	// 全局 AutoApprove="true" → 所有工具应自动批准
	result, _ := EvaluateApprovalRules(session, []ToolCall{{Name: "any_tool", ID: "1"}})
	if result.Decision != DecisionApproved {
		t.Errorf("Expected DecisionApproved with DefaultAutoApprove=true, got %v", result.Decision)
	}
}

// TestEvaluateApprovalRules_RejectPriority 测试拒绝优先于审批
func TestEvaluateApprovalRules_RejectPriority(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "test-agent",
		NowAgent:       "test-agent",
		CurrentAgentConfig: cfgStruct.AgentConfig{
			AutoApprove: "true", // 全部批准
			AutoReject:  "true", // 全部拒绝（应优先）
		},
	}

	// 拒绝规则应优先于审批规则
	result, _ := EvaluateApprovalRules(session, []ToolCall{{Name: "conflict_tool", ID: "1"}})
	if result.Decision != DecisionRejected {
		t.Errorf("Expected DecisionRejected (reject priority), got %v", result.Decision)
	}
}

// TestEvaluateApprovalRules_BuiltinRules 测试内置规则
func TestEvaluateApprovalRules_BuiltinRules(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	// 保存并设置配置：使用内置规则
	oldIgnore := config.GlobalConfig.Agent.IgnoreDefaultRules
	oldApprove := config.GlobalConfig.Agent.DefaultAutoApprove
	oldReject := config.GlobalConfig.Agent.DefaultAutoReject
	defer func() {
		config.GlobalConfig.Agent.IgnoreDefaultRules = oldIgnore
		config.GlobalConfig.Agent.DefaultAutoApprove = oldApprove
		config.GlobalConfig.Agent.DefaultAutoReject = oldReject
	}()

	config.GlobalConfig.Agent.IgnoreDefaultRules = false // 启用内置规则
	config.GlobalConfig.Agent.DefaultAutoApprove = ""
	config.GlobalConfig.Agent.DefaultAutoReject = ""

	session := &storageStructs.Chats{
		ID:                 1,
		DB:                 db,
		CurrentAgentConfig: cfgStruct.AgentConfig{},
	}

	// 内置 approve 规则应批准 scope 工具
	result, _ := EvaluateApprovalRules(session, []ToolCall{{Name: "scope", ID: "1"}})
	if result.Decision != DecisionApproved {
		t.Errorf("Expected DecisionApproved for scope (builtin rule), got %v", result.Decision)
	}

	// 内置 approve 规则应批准 agent 工具
	result, _ = EvaluateApprovalRules(session, []ToolCall{{Name: "agent", ID: "2"}})
	if result.Decision != DecisionApproved {
		t.Errorf("Expected DecisionApproved for agent (builtin rule), got %v", result.Decision)
	}

	// 内置 approve 规则应批准 trace 工具
	result, _ = EvaluateApprovalRules(session, []ToolCall{{Name: "trace", ID: "3"}})
	if result.Decision != DecisionApproved {
		t.Errorf("Expected DecisionApproved for trace (builtin rule), got %v", result.Decision)
	}

	// 内置 approve 规则应批准 edit @task（虚拟任务对象）
	taskPath := any("@task")
	result, _ = EvaluateApprovalRules(session, []ToolCall{{
		Name: "edit", ID: "4",
		Parameters: map[string]*any{"path": &taskPath},
	}})
	if result.Decision != DecisionApproved {
		t.Errorf("Expected DecisionApproved for edit @task (builtin rule), got %v", result.Decision)
	}

	// 内置 approve 规则不应批准普通文件编辑
	normalPath := any("main.go")
	result, _ = EvaluateApprovalRules(session, []ToolCall{{
		Name: "edit", ID: "5",
		Parameters: map[string]*any{"path": &normalPath},
	}})
	if result.Decision != DecisionManual {
		t.Errorf("Expected DecisionManual for normal file edit, got %v", result.Decision)
	}

	// 内置 approve 规则应批准 fetch GET（只自动批准 GET）
	getMethod := any("GET")
	result, _ = EvaluateApprovalRules(session, []ToolCall{{
		Name: "fetch", ID: "6",
		Parameters: map[string]*any{"method": &getMethod},
	}})
	if result.Decision != DecisionApproved {
		t.Errorf("Expected DecisionApproved for fetch GET (builtin rule), got %v", result.Decision)
	}

	// 内置 approve 规则不应批准 fetch POST
	postMethod := any("POST")
	result, _ = EvaluateApprovalRules(session, []ToolCall{{
		Name: "fetch", ID: "7",
		Parameters: map[string]*any{"method": &postMethod},
	}})
	if result.Decision != DecisionManual {
		t.Errorf("Expected DecisionManual for fetch POST, got %v", result.Decision)
	}

	// 内置 approve 规则应批准 run sleep 类型
	sleepType := any("sleep")
	result, _ = EvaluateApprovalRules(session, []ToolCall{{
		Name: "run", ID: "8",
		Parameters: map[string]*any{"type": &sleepType},
	}})
	if result.Decision != DecisionApproved {
		t.Errorf("Expected DecisionApproved for run sleep (builtin rule), got %v", result.Decision)
	}

	// 内置 approve 规则不应批准 run shell 类型
	shellType := any("shell")
	result, _ = EvaluateApprovalRules(session, []ToolCall{{
		Name: "run", ID: "9",
		Parameters: map[string]*any{"type": &shellType},
	}})
	if result.Decision != DecisionManual {
		t.Errorf("Expected DecisionManual for run shell, got %v", result.Decision)
	}
}

// TestEvaluateApprovalRules_BuiltinReject 测试内置拒绝规则（敏感文件路径）
func TestEvaluateApprovalRules_BuiltinReject(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	oldIgnore := config.GlobalConfig.Agent.IgnoreDefaultRules
	defer func() { config.GlobalConfig.Agent.IgnoreDefaultRules = oldIgnore }()
	config.GlobalConfig.Agent.IgnoreDefaultRules = false

	envPath := "/project/.env"
	envVal := any(envPath)

	session := &storageStructs.Chats{
		ID: 1, DB: db,
		CurrentAgentConfig: cfgStruct.AgentConfig{},
	}

	// 编辑 .env → 应被内置拒绝规则匹配
	result, _ := EvaluateApprovalRules(session, []ToolCall{{
		Name: "edit", ID: "1",
		Parameters: map[string]*any{"path": &envVal},
	}})
	if result.Decision != DecisionRejected {
		t.Errorf("Expected DecisionRejected for edit .env, got %v", result.Decision)
	}
}

// TestEvaluateApprovalRules_NilSession 测试空会话
func TestEvaluateApprovalRules_NilSession(t *testing.T) {
	result, _ := EvaluateApprovalRules(nil, []ToolCall{{Name: "test", ID: "1"}})
	if result.Decision != DecisionManual {
		t.Errorf("Expected DecisionManual for nil session, got %v", result.Decision)
	}

	result, _ = EvaluateApprovalRules(&storageStructs.Chats{}, nil)
	if result.Decision != DecisionManual {
		t.Errorf("Expected DecisionManual for nil tools, got %v", result.Decision)
	}

	result, _ = EvaluateApprovalRules(&storageStructs.Chats{}, []ToolCall{})
	if result.Decision != DecisionManual {
		t.Errorf("Expected DecisionManual for empty tools, got %v", result.Decision)
	}
}

// TestMergeAutoRuleExpr 测试 mergeAutoRuleExpr 的各种边界情况
func TestMergeAutoRuleExpr(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		builtin  string
		expected string
	}{
		{"both empty", "", "", ""},
		{"user only", "true", "", "true"},
		{"builtin only", "", "x == 1", "x == 1"},
		{"both non-empty", "a", "b", "truthy(a) || truthy(b)"},
		{"with surrounding spaces", "  true  ", "  x  ", "truthy(true) || truthy(x)"},
		{"user true string", "true", "x == 1", "truthy(true) || truthy(x == 1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeAutoRuleExpr(tt.user, tt.builtin)
			if got != tt.expected {
				t.Errorf("mergeAutoRuleExpr(%q, %q) = %q, want %q",
					tt.user, tt.builtin, got, tt.expected)
			}
		})
	}
}

// TestUserAddMsg_WaitApprove 测试 WaitApprove 状态下用户输入是否同时作为拒绝和正常消息处理
func TestUserAddMsg_WaitApprove(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 创建处于 WaitApprove 状态的会话
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "",
		State:          state.StateWaitApprove,
	}

	// 在 WaitApprove 状态下发送用户消息
	userInput := "继续吧，但别改配置文件"
	err := UserAddMsg(session, userInput, nil)
	if err != nil {
		t.Fatalf("UserAddMsg failed: %v", err)
	}

	// 验证生成了 2 条消息：1 条拒绝 + 1 条用户正常消息
	var messages []storageStructs.Messages
	db.Where("chat_id = ?", 1).Order("id ASC").Find(&messages)

	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages (reject + user), got %d", len(messages))
	}

	// 第 1 条应该是拒绝通信消息
	if messages[0].Type != storageStructs.MessagesRoleCommunicate {
		t.Errorf("Expected first message type Communicate, got %d", messages[0].Type)
	}

	// 第 2 条应该是用户正常消息
	if messages[1].Type != storageStructs.MessagesRoleUser {
		t.Errorf("Expected second message type User, got %d", messages[1].Type)
	}

	// 状态应该回到 Idle
	if session.State != state.StateIdle {
		t.Errorf("Expected state Idle after reject, got %v", session.State)
	}
}

// TestUserAddMsg_WaitApprove_AgentActive 测试有活跃子代理时 WaitApprove 状态的处理
func TestUserAddMsg_WaitApprove_AgentActive(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	// 创建处于 WaitApprove 且有子代理的会话
	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "test-agent",
		NowAgent:       "test-agent",
		State:          state.StateWaitApprove,
	}

	err := UserAddMsg(session, "停下", nil)
	if err != nil {
		t.Fatalf("UserAddMsg failed: %v", err)
	}

	// 验证生成了 1 条拒绝消息（用户输入作为拒绝）
	var messages []storageStructs.Messages
	db.Where("chat_id = ?", 1).Order("id ASC").Find(&messages)
	if len(messages) < 1 {
		t.Fatalf("Expected at least 1 message, got %d", len(messages))
	}

	// 状态回到 Idle
	if session.State != state.StateIdle {
		t.Errorf("Expected state Idle, got %v", session.State)
	}
}

// TestEvaluateApprovalRules_ErrorPropagation 测试错误不会被静默吞咽
func TestEvaluateApprovalRules_ErrorPropagation(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	session := &storageStructs.Chats{
		ID:             1,
		DB:             db,
		CurrentAgentID: "test-agent",
		NowAgent:       "test-agent",
		CurrentAgentConfig: cfgStruct.AgentConfig{
			AutoReject: "invalid syntax {{{",
		},
	}

	_, err := EvaluateApprovalRules(session, []ToolCall{{Name: "any", ID: "1"}})
	if err == nil {
		t.Error("Expected error for malformed reject expression, got nil")
	}
	t.Logf("Got expected error: %v", err)
}

// TestInjectNativeFormatCorrection 验证打回时注入的格式纠正消息：
// 应写入一条 user 类型消息，且内容包含 <tools> 正确格式示例。
func TestInjectNativeFormatCorrection(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	session := &storageStructs.Chats{ID: 7, DB: db}
	if err := injectNativeFormatCorrection(db, session); err != nil {
		t.Fatalf("injectNativeFormatCorrection: %v", err)
	}

	var msgs []storageStructs.Messages
	if err := db.Where("chat_id = ?", 7).Find(&msgs).Error; err != nil {
		t.Fatalf("query messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("应恰好插入 1 条纠正消息，实际 %d", len(msgs))
	}
	if msgs[0].Type != storageStructs.MessagesRoleUser {
		t.Errorf("纠正消息应为 user 类型，实际类型 %d", msgs[0].Type)
	}
	if !strings.Contains(msgs[0].Delta, "<tools>") {
		t.Errorf("纠正消息应包含 <tools> 正确格式示例，实际内容: %q", msgs[0].Delta)
	}
}

// TestErrNativeToolCallFormatPropagation 验证 sentinel error 经
// SimpleOpenAIRequest 的 "callback error: %w" 包装后仍可被 errors.Is 识别，
// 保证 SendRequest 的打回判定在错误传播链路上可靠。
func TestErrNativeToolCallFormatPropagation(t *testing.T) {
	wrapped := fmt.Errorf("callback error: %w", errNativeToolCallFormat)
	if !errors.Is(wrapped, errNativeToolCallFormat) {
		t.Error("包装错误应能通过 errors.Is 识别 errNativeToolCallFormat")
	}
	if errors.Is(context.Canceled, errNativeToolCallFormat) {
		t.Error("errNativeToolCallFormat 不应与 context.Canceled 混淆")
	}
}
