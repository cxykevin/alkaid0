package build

import (
	"fmt"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// TestTitle_Basic 测试首次标题请求构建：第一条用户请求 + 第一条 AI 响应
func TestTitle_Basic(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	messages := []structs.Messages{
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "帮我实现自动标题总结"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "好的，我来实现"},
	}
	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	request, err := Title(1, db)
	if err != nil {
		t.Fatalf("Title failed: %v", err)
	}
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// 模型配置
	if request.Model != "test-chat" {
		t.Errorf("Expected model 'test-chat', got '%s'", request.Model)
	}
	if !request.Stream {
		t.Error("Expected stream to be true")
	}
	wantTitleTokens := completionTokenLimit(titleMaxToken)
	if request.MaxTokens != nil {
		t.Errorf("不应再发送 max_tokens，实际 %d", *request.MaxTokens)
	}
	if request.MaxCompletionTokens == nil || *request.MaxCompletionTokens != wantTitleTokens {
		t.Errorf("Expected max_completion_tokens %d, got %v", wantTitleTokens, request.MaxCompletionTokens)
	}
	if request.Temperature == nil || *request.Temperature != 0.7 {
		t.Error("Expected temperature to be 0.7")
	}
	if request.TopP == nil || *request.TopP != 0.9 {
		t.Error("Expected top_p to be 0.9")
	}

	// 消息结构：system + user 请求 + assistant 回复 + 标题指令
	if len(request.Messages) != 4 {
		t.Fatalf("Expected 4 messages, got %d", len(request.Messages))
	}
	if request.Messages[0].Role != "system" {
		t.Errorf("Messages[0] should be system, got %s", request.Messages[0].Role)
	}
	if request.Messages[1].Role != "user" || request.Messages[1].Content != "帮我实现自动标题总结" {
		t.Errorf("Messages[1] should be the user request, got role=%s content=%q", request.Messages[1].Role, request.Messages[1].Content)
	}
	if request.Messages[2].Role != "assistant" || request.Messages[2].Content != "好的，我来实现" {
		t.Errorf("Messages[2] should be the assistant reply, got role=%s content=%q", request.Messages[2].Role, request.Messages[2].Content)
	}
	if request.Messages[3].Role != "user" || request.Messages[3].Content != prompts.Title {
		t.Errorf("Messages[3] should be the title instruction, got role=%s", request.Messages[3].Role)
	}

	// 推理与思考特判
	if request.ReasoningEffort == nil || *request.ReasoningEffort != "low" {
		t.Error("Expected reasoning effort to be low")
	}
	if request.Thinking == nil || request.Thinking.Type != "disabled" {
		t.Error("Expected thinking to be disabled")
	}
}

// TestTitle_InsufficientMessages 测试只有用户消息（无 AI 响应）时不构建
func TestTitle_InsufficientMessages(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "hello"}).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	request, err := Title(1, db)
	if err != nil {
		t.Fatalf("Title failed: %v", err)
	}
	if request != nil {
		t.Fatal("Expected nil request with insufficient messages")
	}
}

// TestTitle_NoMessages 测试空会话不构建
func TestTitle_NoMessages(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	request, err := Title(1, db)
	if err != nil {
		t.Fatalf("Title failed: %v", err)
	}
	if request != nil {
		t.Fatal("Expected nil request with no messages")
	}
}

// TestTitle_FirstOfEach 测试"第一条"语义：乱序插入时取各自类型的第一条
func TestTitle_FirstOfEach(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	// agent(id=1)、user(id=2)、user(id=3)
	messages := []structs.Messages{
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "第一条回复"},
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "第一条请求"},
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "第二条请求"},
	}
	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	request, err := Title(1, db)
	if err != nil {
		t.Fatalf("Title failed: %v", err)
	}
	if request == nil {
		t.Fatal("Expected non-nil request")
	}
	if request.Messages[1].Content != "第一条请求" {
		t.Errorf("Messages[1] should be the first user request, got %q", request.Messages[1].Content)
	}
	if request.Messages[2].Content != "第一条回复" {
		t.Errorf("Messages[2] should be the first assistant reply, got %q", request.Messages[2].Content)
	}
}

// TestTitle_SkipsEmptyDelta 测试空正文占位消息被跳过
func TestTitle_SkipsEmptyDelta(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	// 第一条 user 为空正文（占位），第二条有内容；agent 同理
	messages := []structs.Messages{
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: ""},
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "有内容的请求"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: ""},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "有内容的回复"},
	}
	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	request, err := Title(1, db)
	if err != nil {
		t.Fatalf("Title failed: %v", err)
	}
	if request == nil {
		t.Fatal("Expected non-nil request")
	}
	if request.Messages[1].Content != "有内容的请求" {
		t.Errorf("Messages[1] should skip empty-delta user message, got %q", request.Messages[1].Content)
	}
	if request.Messages[2].Content != "有内容的回复" {
		t.Errorf("Messages[2] should skip empty-delta agent message, got %q", request.Messages[2].Content)
	}
}

// TestTitle_SubagentFirst 测试首条 AI 回复为子代理回复（带 AgentID）时仍命中
func TestTitle_SubagentFirst(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	subAgent := "sub-agent-1"
	messages := []structs.Messages{
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "让子代理做"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "子代理的回复", AgentID: &subAgent},
	}
	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	request, err := Title(1, db)
	if err != nil {
		t.Fatalf("Title failed: %v", err)
	}
	if request == nil {
		t.Fatal("Expected non-nil request")
	}
	if request.Messages[2].Content != "子代理的回复" {
		t.Errorf("Messages[2] should include subagent reply, got %q", request.Messages[2].Content)
	}
}

// TestTitle_ModelFallback 测试 TitleModel 未命中时回退 DefaultModelID
func TestTitle_ModelFallback(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 999
	db := setupTestDB(t)

	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "hi"}).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "hello"}).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	request, err := Title(1, db)
	if err != nil {
		t.Fatalf("Title failed: %v", err)
	}
	if request == nil {
		t.Fatal("Expected non-nil request")
	}
	if request.Model != "test-chat" {
		t.Errorf("Expected fallback model 'test-chat', got '%s'", request.Model)
	}
}

// TestTitle_ModelNotFound 测试无可用模型时报错
func TestTitle_ModelNotFound(t *testing.T) {
	config.GlobalConfigSwap(cfgStruct.Config{
		Model: cfgStruct.ModelsConfig{
			DefaultModelID: 1,
			Models:         map[int32]cfgStruct.ModelConfig{},
		},
	})
	config.GlobalConfig.Agent.TitleModel = 999
	db := setupTestDB(t)

	// 消息充足，确保流程走到模型解析阶段
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "hi"}).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "hello"}).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	request, err := Title(1, db)
	if err == nil {
		t.Fatal("Expected error when no model is available")
	}
	if request != nil {
		t.Fatal("Expected nil request on error")
	}
}

// TestTitleFull_AllMessages 测试完整对话重生成：对话消息按序纳入、工具结果不计入、指令在最后
func TestTitleFull_AllMessages(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	messages := []structs.Messages{
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "请求一"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "回复一"},
		{ChatID: 1, Type: structs.MessagesRoleTool, Delta: `[{"return":"工具结果"}]`},
		{ChatID: 1, Type: structs.MessagesRoleCommunicate, Delta: "拒绝原因"},
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "请求二"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "回复二"},
	}
	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	request, err := TitleFull(1, db)
	if err != nil {
		t.Fatalf("TitleFull failed: %v", err)
	}
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// system + 5 条对话消息 + 标题指令（工具结果行既不算一条，也不进上下文）
	if len(request.Messages) != 7 {
		t.Fatalf("Expected 7 messages, got %d", len(request.Messages))
	}
	if request.Messages[0].Role != "system" {
		t.Errorf("Messages[0] should be system, got %s", request.Messages[0].Role)
	}
	// 顺序：user, assistant, communicate, user, assistant, 指令
	expects := []struct {
		role    string
		content string
	}{
		{"user", "请求一"},
		{"assistant", "回复一"},
		{"user", "拒绝原因"},
		{"user", "请求二"},
		{"assistant", "回复二"},
	}
	for i, e := range expects {
		if request.Messages[i+1].Role != e.role || request.Messages[i+1].Content != e.content {
			t.Errorf("Messages[%d] mismatch: got role=%s content=%q, want role=%s content=%q",
				i+1, request.Messages[i+1].Role, request.Messages[i+1].Content, e.role, e.content)
		}
	}
	if request.Messages[6].Role != "user" || request.Messages[6].Content != prompts.Title {
		t.Errorf("Messages[6] should be the title instruction, got role=%s", request.Messages[6].Role)
	}
}

// TestTitleFull_WindowLimit 验证标题重生成只取最近 titleWindowMessages 条对话消息：
// 更旧的消息被窗口挤掉，工具结果行既不占窗口也不出现在上下文里。
func TestTitleFull_WindowLimit(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	// 5 条最旧的 user 消息（应被挤出窗口），随后 60 组「user 消息 + 工具结果行」
	for i := range 5 {
		if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleUser, Delta: fmt.Sprintf("旧-%d", i)}).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}
	for i := range 60 {
		msgs := []structs.Messages{
			{ChatID: 1, Type: structs.MessagesRoleUser, Delta: fmt.Sprintf("新-%d", i)},
			{ChatID: 1, Type: structs.MessagesRoleTool, Delta: `[{"return":"工具结果"}]`},
		}
		for _, msg := range msgs {
			if err := db.Create(&msg).Error; err != nil {
				t.Fatalf("Failed to create test message: %v", err)
			}
		}
	}

	request, err := TitleFull(1, db)
	if err != nil {
		t.Fatalf("TitleFull failed: %v", err)
	}
	if request == nil {
		t.Fatal("Expected non-nil request")
	}

	// system + titleWindowMessages 条对话消息 + 标题指令
	if len(request.Messages) != titleWindowMessages+2 {
		t.Fatalf("Expected %d messages, got %d", titleWindowMessages+2, len(request.Messages))
	}
	// 窗口取最新的 50 条：最早一条是 新-10，最新一条是 新-59
	if got := request.Messages[1].Content; got != "新-10" {
		t.Errorf("Expected oldest kept message 新-10, got %q", got)
	}
	if got := request.Messages[len(request.Messages)-2].Content; got != "新-59" {
		t.Errorf("Expected newest message 新-59, got %q", got)
	}
	for i, m := range request.Messages {
		if m.Content == `[{"return":"工具结果"}]` {
			t.Errorf("Messages[%d] must not carry tool results", i)
		}
	}
}

// TestTitleFull_NoMessages 测试空会话不构建
func TestTitleFull_NoMessages(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	request, err := TitleFull(1, db)
	if err != nil {
		t.Fatalf("TitleFull failed: %v", err)
	}
	if request != nil {
		t.Fatal("Expected nil request with no messages")
	}
}

// TestTitleFull_OnlyEmptyDelta 测试只有空正文占位消息时不构建
func TestTitleFull_OnlyEmptyDelta(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.TitleModel = 1
	db := setupTestDB(t)

	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: ""}).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	request, err := TitleFull(1, db)
	if err != nil {
		t.Fatalf("TitleFull failed: %v", err)
	}
	if request != nil {
		t.Fatal("Expected nil request with only empty-delta messages")
	}
}
