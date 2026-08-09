package build

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/parser"
	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupTestDB 设置测试数据库
func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	// 自动迁移表结构
	err = db.AutoMigrate(&structs.Messages{})
	if err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	return db
}

// setupTestConfig 设置测试配置
func setupTestConfig() {
	// 设置测试模型配置
	config.GlobalConfigSwap(cfgStruct.Config{
		Model: cfgStruct.ModelsConfig{
			DefaultModelID: 1,
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName: "test-model",
					ModelID:   "test-chat",

					ModelTemperature:  0.7,
					ModelTopP:         0.9,
					EnableThinking:    true,
					EnableToolCalling: true,
					ProviderSpecificConfig: cfgStruct.ProviderSpecificConfig{
						EnableDeepseekThinking: true,
						EnableReasoningEffort:  true,
						EnableTopP:             true,
						EnableTopK:             true,
						EnableTemperature:      true,
					},
				},
				2: {
					ModelName:         "test-model-no-think",
					ModelID:           "test-chat",
					ModelTemperature:  0.7,
					ModelTopP:         0.9,
					EnableThinking:    false,
					EnableToolCalling: true,
					ProviderSpecificConfig: cfgStruct.ProviderSpecificConfig{
						EnableDeepseekThinking: true,
						EnableReasoningEffort:  true,
						EnableTopP:             true,
						EnableTopK:             true,
						EnableTemperature:      true,
					},
				},
			},
		},
		Agent: cfgStruct.AgentsConfig{
			Agents: map[string]cfgStruct.AgentConfig{
				"test-agent": {
					AgentName:        "Test Agent",
					AgentPrompt:      "You are a test agent",
					AgentModel:       1,
					AgentDescription: "A test agent for unit testing",
				},
			},
			GlobalPrompt: "You are a helpful assistant",
		},
	})
}

// TestRequestBody_Basic 测试基本功能
func TestRequestBody_Basic(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

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

	// 定义测试工具
	toolsList := []*parser.ToolsDefine{
		{
			Name:        "test_tool",
			Description: "A test tool",
			Parameters: map[string]parser.ToolParameters{
				"input": {
					Type:        parser.ToolTypeString,
					Description: "Input parameter",
				},
			},
		},
	}

	// 调用 RequestBody
	request, err := RequestBody(1, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	// 验证请求结构
	if request.Model != "test-chat" {
		t.Errorf("Expected model ID 'test-chat', got '%s'", request.Model)
	}

	if !request.Stream {
		t.Error("Expected stream to be true")
	}

	if *request.Temperature != 0.7 {
		t.Errorf("Expected temperature 0.7, got %f", *request.Temperature)
	}

	if *request.TopP != 0.9 {
		t.Errorf("Expected top_p 0.9, got %f", *request.TopP)
	}

	if *request.MaxTokens != maxToken {
		t.Errorf("Expected max_tokens %d, got %d", maxToken, *request.MaxTokens)
	}

	// 验证消息数量（应该包含 1 个合并后的系统消息和 2 个对话消息）
	expectedMsgCount := 3 // 1 merged system message + 2 conversation messages
	if len(request.Messages) != expectedMsgCount {
		t.Errorf("Expected %d messages, got %d", expectedMsgCount, len(request.Messages))
		for i, msg := range request.Messages {
			t.Logf("Message %d: Role=%s, Content=%s", i, msg.Role, msg.Content[:min(50, len(msg.Content))])
		}
	}

	// 验证系统消息顺序
	if request.Messages[0].Role != "system" {
		t.Errorf("First message should be system, got %s", request.Messages[0].Role)
	}

	// 验证合并后的内容包含关键信息
	content := request.Messages[0].Content
	if !strings.Contains(content, "test-model") {
		t.Errorf("System message should contain model name, got: %s", content)
	}
	if !strings.Contains(content, "You are a helpful assistant") {
		t.Errorf("System message should contain global prompt, got: %s", content)
	}
}

// TestRequestBody_Real 测试真实api
func TestRequestBody_Real(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

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

	// 定义测试工具
	toolsList := []*parser.ToolsDefine{
		{
			Name:        "test_tool",
			Description: "A test tool",
			Parameters: map[string]parser.ToolParameters{
				"input": {
					Type:        parser.ToolTypeString,
					Description: "Input parameter",
				},
			},
		},
	}

	// 调用 RequestBody
	agentCfg, _ := agentconfig.GetAgentConfig("test-agent")
	request, err := RequestBody(1, 1, "test-agent", &toolsList, db, "", "", agentCfg, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	v, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("RequestBody json failed: %v", err)
	}
	fmt.Println(string(v))
}

// TestRequestBody_NoAgent 测试没有代理的情况
func TestRequestBody_NoAgent(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 插入测试消息
	message := structs.Messages{
		ChatID: 2,
		Type:   structs.MessagesRoleUser,
		Delta:  "Test message",
	}

	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	toolsList := []*parser.ToolsDefine{}

	// 调用 RequestBody，不指定代理
	request, err := RequestBody(2, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	// 验证使用了默认代理（在合并后的 system 消息中）
	foundDefaultAgent := false
	for _, msg := range request.Messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "You are a helpful assistant") {
			foundDefaultAgent = true
			break
		}
	}

	if !foundDefaultAgent {
		t.Error("Expected to find default agent message")
	}
}

// TestRequestBody_WithThinking 测试包含思考内容的情况
func TestRequestBody_WithThinking(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 插入包含思考的消息
	message := structs.Messages{
		ChatID:        3,
		Type:          structs.MessagesRoleAgent,
		Delta:         "Final answer",
		ThinkingDelta: "Let me think about this...",
	}

	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	toolsList := []*parser.ToolsDefine{}

	request, err := RequestBody(3, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	// 验证思考内容被正确处理
	foundThinking := false
	for _, msg := range request.Messages {
		if msg.ReasoningContent != nil && *msg.ReasoningContent == "Let me think about this..." {
			foundThinking = true
			break
		}
	}

	if !foundThinking {
		t.Error("Expected to find reasoning content")
	}
}

// TestRequestBody_WithSummary 测试包含摘要的情况
func TestRequestBody_WithSummary(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 插入包含摘要的消息
	messages := []structs.Messages{
		{
			ChatID: 4,
			Type:   structs.MessagesRoleUser,
			Delta:  "Old message 1",
		},
		{
			ChatID: 4,
			Type:   structs.MessagesRoleAgent,
			Delta:  "Old response 1",
		},
		{
			ChatID:  4,
			Type:    structs.MessagesRoleUser,
			Delta:   "New message",
			Summary: "Previous conversation summary",
		},
	}

	for _, msg := range messages {
		if err := db.Create(&msg).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
	}

	toolsList := []*parser.ToolsDefine{}

	request, err := RequestBody(4, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	// 验证摘要消息存在且在正确位置
	foundSummary := false
	for _, msg := range request.Messages {
		// 摘要会被包装在模板中，所以我们检查是否包含摘要内容
		if msg.Content != "" &&
			(strings.Contains(msg.Content, "Previous conversation summary") ||
				strings.Contains(msg.Content, "Context summary")) {
			foundSummary = true
			// 摘要应该在用户消息之前
			// if i >= len(request.Messages)-1 {
			// 	t.Error("Summary should not be the last message")
			// }
			break
		}
	}

	if !foundSummary {
		t.Error("Expected to find summary message")
		// 打印所有消息内容以便调试
		for i, msg := range request.Messages {
			t.Logf("Message %d: Role=%s, Content=%s", i, msg.Role, msg.Content)
		}
	}
}

// TestRequestBody_InvalidModel 测试无效模型ID
func TestRequestBody_InvalidModel(t *testing.T) {
	// 设置一个没有默认模型的配置
	config.GlobalConfigSwap(cfgStruct.Config{
		Model: cfgStruct.ModelsConfig{
			DefaultModelID: 999, // 不存在的默认模型
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
		Agent: cfgStruct.AgentsConfig{
			Agents: map[string]cfgStruct.AgentConfig{
				"test-agent": {
					AgentName:        "Test Agent",
					AgentPrompt:      "You are a test agent",
					AgentModel:       1,
					AgentDescription: "A test agent for unit testing",
				},
			},
			GlobalPrompt: "You are a helpful assistant",
		},
	})

	db := setupTestDB(t)

	toolsList := []*parser.ToolsDefine{}

	// 使用不存在的模型ID
	agentCfg, _ := agentconfig.GetAgentConfig("test-agent")
	_, err := RequestBody(1, 999, "test-agent", &toolsList, db, "", "", agentCfg, &structs.Chats{})
	if err == nil {
		t.Error("Expected error for invalid model ID")
	}

	// 恢复正常配置
	setupTestConfig()
}

// // TestRequestBody_InvalidAgent 测试无效代理ID
// func TestRequestBody_InvalidAgent(t *testing.T) {
// 	setupTestConfig()
// 	db := setupTestDB(t)

// 	toolsList := []*parser.ToolsDefine{}

// 	// 使用不存在的代理ID
// 	_, err := RequestBody(1, 1, "invalid-agent", &toolsList, db, "", "", cfgStruct.AgentConfig{})
// 	if err == nil {
// 		t.Error("Expected error for invalid agent ID")
// 	}
// }o-m90y9

// // TestRequestBody_ToolMarshalError 测试工具序列化错误
// func TestRequestBody_ToolMarshalError(t *testing.T) {
// 	setupTestConfig()
// 	db := setupTestDB(t)

// 	// 创建一个无法序列化的工具（包含循环引用）
// 	type CircularRef struct {
// 		Self *CircularRef
// 	}

// 	circular := &CircularRef{}
// 	circular.Self = circular // 创建循环引用

// 	invalidTools := []*parser.ToolsDefine{
// 		{
// 			Name: "invalid_tool",
// 			Parameters: map[string]parser.ToolParameters{
// 				"circular": {
// 					Type: parser.ToolTypeObject,
// 				},
// 			},
// 		},
// 	}

// 	// 手动修改工具以包含循环引用
// 	invalidTools[0].Parameters["circular"] = parser.ToolParameters{
// 		Type: parser.ToolTypeObject,
// 	}

// 	// 由于JSON marshal在Go中实际上可以处理很多情况，我们改为测试一个nil的工具列表
// 	var nilTools *[]*parser.ToolsDefine = nil

// 	_, err := RequestBody(1, 1, "test-agent", nilTools, db)
// 	if err != nil {
// 		t.Errorf("Unexpected error with nil tools: %v", err)
// 	}
// }

// TestRequestBody_EmptyMessages 测试空消息列表
func TestRequestBody_EmptyMessages(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	toolsList := []*parser.ToolsDefine{}

	// 不插入任何消息
	agentCfg, _ := agentconfig.GetAgentConfig("test-agent")
	request, err := RequestBody(5, 1, "test-agent", &toolsList, db, "", "", agentCfg, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	// 应该只有 1 个合并后的系统消息
	expectedMsgCount := 1 // 1 merged system message
	if len(request.Messages) != expectedMsgCount {
		t.Errorf("Expected %d messages for empty chat, got %d", expectedMsgCount, len(request.Messages))
	}
}

// TestRequestBody_ManyMessages 测试大量消息（分页）
func TestRequestBody_ManyMessages(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// 插入超过单页数量的消息
	for i := range 50 {
		message := structs.Messages{
			ChatID: 6,
			Type:   structs.MessagesRoleUser,
			Delta:  "Message " + string(rune(i)),
		}
		if i%2 == 1 {
			message.Type = structs.MessagesRoleAgent
			message.Delta = "Response " + string(rune(i))
		}
		if err := db.Create(&message).Error; err != nil {
			t.Fatalf("Failed to create test message %d: %v", i, err)
		}
	}

	toolsList := []*parser.ToolsDefine{}

	agentCfg, _ := agentconfig.GetAgentConfig("test-agent")
	request, err := RequestBody(6, 1, "test-agent", &toolsList, db, "", "", agentCfg, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	// 验证消息数量不超过最大限制
	if len(request.Messages) > readPageSize*maxPage+5 { // +5 for system messages
		t.Errorf("Too many messages returned: %d", len(request.Messages))
	}
}

// TestRequestBody_ToolMessage 测试工具类型消息（模式感知）：
// 两种模式下工具结果都走原来的 <tools_return> 文本拼法（映射为 user 角色），
// 原生模式不引入 role:"tool" 消息 / tool_call_id 字段。
func TestRequestBody_ToolMessage(t *testing.T) {
	db := setupTestDB(t)

	toolsList := []*parser.ToolsDefine{}

	buildReq := func(chatID uint32, native bool, delta string) *reqStruct.ChatCompletionRequest {
		setupTestConfig()
		cfg := *config.GlobalConfig
		m := cfg.Model.Models[cfg.Model.DefaultModelID]
		m.EnableToolCalling = native
		cfg.Model.Models[cfg.Model.DefaultModelID] = m
		config.GlobalConfigSwap(cfg)
		if err := db.Create(&structs.Messages{ChatID: chatID, Type: structs.MessagesRoleTool, Delta: delta}).Error; err != nil {
			t.Fatalf("Failed to create test message: %v", err)
		}
		request, err := RequestBody(chatID, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
		if err != nil {
			t.Fatalf("RequestBody failed: %v", err)
		}
		return request
	}

	// 提示词模式：工具结果映射为 user 角色，内容含 <tools_return> 段
	req := buildReq(7, false, "Tool result")
	foundToolMsg := false
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, "Tool result") && strings.Contains(msg.Content, "<tools_return>") && msg.Role == "user" {
			foundToolMsg = true
			break
		}
	}
	if !foundToolMsg {
		t.Error("提示词模式：Expected tool message mapped to user role with <tools_return> wrapper")
	}

	// 原生模式：同样走 <tools_return> 文本拼法，不产生 role:"tool" 消息
	nreq := buildReq(8, true, `[{"name":"edit","id":"call_1","return":"{\"ok\":true}"},{"name":"edit","id":"call_2","return":"{\"ok\":false}"}]`)
	var toolMsgs []reqStruct.Message
	for _, msg := range nreq.Messages {
		if msg.Role == reqStruct.RoleTool {
			toolMsgs = append(toolMsgs, msg)
		}
	}
	if len(toolMsgs) != 0 {
		t.Errorf("原生模式：should NOT emit role:tool messages, got %d", len(toolMsgs))
	}
	nfound := false
	for _, msg := range nreq.Messages {
		// return 字段值是 JSON 序列化字符串（转义后的对象），如 {\"ok\":true}
		if msg.Role == "user" && strings.Contains(msg.Content, "<tools_return>") && strings.Contains(msg.Content, `{\"ok\":true}`) {
			nfound = true
			break
		}
	}
	if !nfound {
		t.Error("原生模式：Expected tool result as user role with <tools_return> wrapper")
	}
}

// TestRequestBody_NativeHistoryReplay 原生模式完整历史回放：
// assistant 工具调用以 <tools> 文本段拼回 content（不引入原生 tool_calls/tool_call_id），
// 工具结果以 <tools_return> 文本段回放（user 角色），顺序保持原始。
func TestRequestBody_NativeHistoryReplay(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	m.EnableToolCalling = true
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 9, Type: structs.MessagesRoleUser, Delta: "call a tool"},
		{ChatID: 9, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_1","parameters":{"path":"a.txt","text":"hi"}}]`},
		{ChatID: 9, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_1","return":"{\"ok\":true}"}]`},
		{ChatID: 9, Type: structs.MessagesRoleUser, Delta: "continue"},
	}
	for _, mm := range msgs {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	req, err := RequestBody(9, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	var assistantWithTools, toolReturn bool
	var assistantHasNativeToolCalls bool
	for _, msg := range req.Messages {
		switch msg.Role {
		case "assistant":
			if strings.Contains(msg.Content, "<tools>") && strings.Contains(msg.Content, `"name":"edit"`) {
				assistantWithTools = true
			}
			if len(msg.ToolCalls) > 0 {
				assistantHasNativeToolCalls = true
			}
		case "user":
			if strings.Contains(msg.Content, "<tools_return>") && strings.Contains(msg.Content, `{\"ok\":true}`) {
				toolReturn = true
			}
		}
	}
	if !assistantWithTools {
		t.Error("assistant history should replay tool call as <tools> text segment")
	}
	if assistantHasNativeToolCalls {
		t.Error("assistant history should NOT carry native tool_calls (no tool_call_id)")
	}
	if !toolReturn {
		t.Error("tool result should replay as <tools_return> text segment")
	}
}
