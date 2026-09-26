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
	"github.com/cxykevin/alkaid0/tools/tools/trace"
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

					ModelTemperature: 0.7,
					ModelTopP:        0.9,
					EnableThinking:   true,
					ProviderSpecificConfig: cfgStruct.ProviderSpecificConfig{
						EnableDeepseekThinking: true,
						EnableReasoningEffort:  true,
						EnableTopP:             true,
						EnableTopK:             true,
						EnableTemperature:      true,
					},
				},
				2: {
					ModelName:        "test-model-no-think",
					ModelID:          "test-chat",
					ModelTemperature: 0.7,
					ModelTopP:        0.9,
					EnableThinking:   false,
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

	// 最大输出 token 数走 max_completion_tokens（不再发 max_tokens）
	if request.MaxTokens != nil {
		t.Errorf("不应再发送 max_tokens，实际 %d", *request.MaxTokens)
	}
	if request.MaxCompletionTokens == nil || *request.MaxCompletionTokens != defaultCompletionTokens {
		t.Errorf("Expected max_completion_tokens %d, got %v", defaultCompletionTokens, request.MaxCompletionTokens)
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
	if strings.Count(content, "Use only the native function schema supplied by the API") != 1 {
		t.Errorf("System message should include the native tool protocol exactly once, got: %s", content)
	}
	if strings.Contains(content, "DO NOT CALL TOOLS WITHOUT EMPTY OBJECT OR NOTHING!") {
		t.Errorf("System message should not contain the deprecated native tool protocol, got: %s", content)
	}
	if len(request.Tools) != 1 || request.Tools[0].Function.Name != "test_tool" {
		t.Errorf("Expected native tool schema for test_tool, got: %#v", request.Tools)
	}
	if request.ToolChoice != "auto" {
		t.Errorf("Expected native tool choice auto, got %q", request.ToolChoice)
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

// TestRequestBody_ThinkingEmptyKeepsField 回归：DeepSeek thinking 模式下，assistant 历史消息
// 即使 ThinkingDelta 为空，回放时也必须保留 reasoning_content 字段（空串占位即可通过校验），
// 否则 OpenAI→Anthropic 转换代理端 400 "The content[].thinking in the thinking mode must be passed back to the API"。
func TestRequestBody_ThinkingEmptyKeepsField(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	// ThinkingDelta 为空但带工具调用历史的 assistant 消息（如历史 thinking 未落库/被清空）
	message := structs.Messages{
		ChatID:                77,
		Type:                  structs.MessagesRoleAgent,
		Delta:                 "调用工具",
		ToolCallingJSONString: `[{"name":"read","id":"call_a","parameters":{"path":"/tmp/x"}}]`,
	}
	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	toolsList := []*parser.ToolsDefine{}
	request, err := RequestBody(77, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	var asst *reqStruct.Message
	for i := range request.Messages {
		if request.Messages[i].Role == reqStruct.RoleAssistant && len(request.Messages[i].ToolCalls) > 0 {
			asst = &request.Messages[i]
			break
		}
	}
	if asst == nil {
		t.Fatal("expected an assistant message with tool_calls")
	}
	if asst.ReasoningContent == nil {
		t.Error("thinking 模式下 assistant 消息必须携带 reasoning_content 字段（空串占位），当前为 nil")
	} else if *asst.ReasoningContent != "" {
		t.Errorf("expected empty placeholder reasoning_content, got %q", *asst.ReasoningContent)
	}
}

// TestRequestBody_EmptyThinkingPlaceholderSkipped thinking 模式下 ThinkingDelta 为空的
// 纯空 assistant（无正文、无工具调用）应被跳过，不残留仅含空 reasoning_content 占位的消息。
func TestRequestBody_EmptyThinkingPlaceholderSkipped(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	message := structs.Messages{
		ChatID: 78,
		Type:   structs.MessagesRoleAgent,
		Delta:  "",
		// ThinkingDelta 为空
	}
	if err := db.Create(&message).Error; err != nil {
		t.Fatalf("Failed to create test message: %v", err)
	}

	toolsList := []*parser.ToolsDefine{}
	request, err := RequestBody(78, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	for _, m := range request.Messages {
		if m.Role == reqStruct.RoleAssistant {
			t.Errorf("空 assistant 消息不应被回放（skip），实际出现: %+v", m)
		}
	}
}

// TestRequestBody_SummaryKeepsThinkingField 回归：压缩完成后摘要内容会被回放到承载它的
// 历史消息上，而截断点消息可能是 assistant（lastMsgID 只看消息顺序、不看类型）。
// thinking 模式下 assistant 消息必须携带 reasoning_content 字段（摘要不是思考产物，
// 空串占位即可），否则压缩后的下一次请求即 400
// "The content[].thinking in the thinking mode must be passed back to the API"。
func TestRequestBody_SummaryKeepsThinkingField(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	if err := db.Create(&structs.Messages{
		ChatID:  79,
		Type:    structs.MessagesRoleAgent,
		Delta:   "old reply",
		Summary: "previous conversation summary",
	}).Error; err != nil {
		t.Fatalf("Failed to create summary message: %v", err)
	}

	toolsList := []*parser.ToolsDefine{}
	request, err := RequestBody(79, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	var asst *reqStruct.Message
	for i := range request.Messages {
		if request.Messages[i].Role == reqStruct.RoleAssistant &&
			strings.Contains(request.Messages[i].Content, "previous conversation summary") {
			asst = &request.Messages[i]
			break
		}
	}
	if asst == nil {
		t.Fatal("expected an assistant message replaying the summary content")
	}
	if asst.ReasoningContent == nil {
		t.Error("thinking 模式下摘要回放的 assistant 消息必须携带 reasoning_content 字段（空串占位），当前为 nil")
	} else if *asst.ReasoningContent != "" {
		t.Errorf("expected empty placeholder reasoning_content for summary, got %q", *asst.ReasoningContent)
	}

	// 线级契约：omitempty 只省略 nil 指针，空串占位必须真的序列化出该字段
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if !strings.Contains(string(payload), "\"reasoning_content\":\"\"") {
		t.Error("摘要回放的 assistant 消息序列化后缺少 reasoning_content 字段（空串占位）")
	}

	// 非 thinking 模型不注入该字段（与普通 assistant 回放保持一致）
	requestNoThink, err := RequestBody(79, 2, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody (no-think model) failed: %v", err)
	}
	for _, m := range requestNoThink.Messages {
		if m.Role == reqStruct.RoleAssistant && strings.Contains(m.Content, "previous conversation summary") {
			if m.ReasoningContent != nil {
				t.Errorf("非 thinking 模型不应注入 reasoning_content，got %q", *m.ReasoningContent)
			}
		}
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
					ModelName:        "test-model",
					ModelID:          "test-model-id",
					ModelTemperature: 0.7,
					ModelTopP:        0.9,
					EnableThinking:   true,
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

// TestRequestBody_ToolMessage 原生模式按 id 拆分为 role:"tool" 消息并严格配对，
// 无对应 assistant 调用的结果丢弃。
func TestRequestBody_ToolMessage(t *testing.T) {
	db := setupTestDB(t)

	toolsList := []*parser.ToolsDefine{}
	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	if err := db.Create(&structs.Messages{
		ChatID:                8,
		Type:                  structs.MessagesRoleAgent,
		Delta:                 "",
		ToolCallingJSONString: `[{"name":"edit","id":"call_1","parameters":{"ok":true}},{"name":"edit","id":"call_2","parameters":{"ok":false}}]`,
	}).Error; err != nil {
		t.Fatalf("create assistant msg: %v", err)
	}
	if err := db.Create(&structs.Messages{
		ChatID: 8, Type: structs.MessagesRoleTool,
		Delta: `[{"name":"edit","id":"call_1","return":"{\"ok\":true}"},{"name":"edit","id":"call_2","return":"{\"ok\":false}"},{"name":"edit","id":"call_ghost","return":"{\"no\":true}"}]`,
	}).Error; err != nil {
		t.Fatalf("create tool result: %v", err)
	}

	req, err := RequestBody(8, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	var toolMsgs []reqStruct.Message
	for _, msg := range req.Messages {
		if msg.Role == reqStruct.RoleTool {
			toolMsgs = append(toolMsgs, msg)
		}
	}
	if len(toolMsgs) != 2 {
		t.Fatalf("原生模式：expected 2 paired role:tool messages (call_ghost dropped), got %d", len(toolMsgs))
	}
	if toolMsgs[0].ToolCallID != "call_1" || !strings.Contains(toolMsgs[0].Content, `{"ok":true}`) {
		t.Errorf("原生模式：first tool msg mismatch: %+v", toolMsgs[0])
	}
	if toolMsgs[1].ToolCallID != "call_2" || !strings.Contains(toolMsgs[1].Content, `{"ok":false}`) {
		t.Errorf("原生模式：second tool msg mismatch: %+v", toolMsgs[1])
	}
}

// TestRequestBody_NativeHistoryReplay 原生模式完整历史回放：
// assistant 工具调用以原生 tool_calls 回放（id/name/arguments，content 保留文本），
// 工具结果按 id 拆分为 role:"tool" 消息（tool_call_id 严格配对），顺序保持原始，无 <tools> 文本段。
func TestRequestBody_NativeHistoryReplay(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
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
	var wrongTextSeg bool
	var asstIdx, toolIdx, continueIdx = -1, -1, -1
	for i, msg := range req.Messages {
		switch msg.Role {
		case "assistant":
			if strings.Contains(msg.Content, "<tools>") {
				wrongTextSeg = true
			}
			if len(msg.ToolCalls) > 0 {
				tc := msg.ToolCalls[0]
				if tc.ID == "call_1" && tc.Type == "function" && tc.Function != nil &&
					tc.Function.Name == "edit" && tc.Function.Arguments == `{"path":"a.txt","text":"hi"}` {
					assistantWithTools = true
					asstIdx = i
				}
			}
		case reqStruct.RoleTool:
			if msg.ToolCallID == "call_1" && strings.Contains(msg.Content, `{"ok":true}`) {
				toolReturn = true
				toolIdx = i
			}
		case "user":
			// user 消息被 UserWrapTemplate 包裹为 <user_prompt>...</user_prompt>
			if strings.Contains(msg.Content, "continue") {
				continueIdx = i
			}
		}
	}
	if wrongTextSeg {
		t.Error("assistant history should NOT contain <tools> text segment")
	}
	if !assistantWithTools {
		t.Error("assistant history should carry native tool_calls (id/name/arguments)")
	}
	if !toolReturn {
		t.Error("tool result should replay as role:tool message with matching tool_call_id")
	}
	// 顺序：assistant(工具调用) → role:tool(结果) → user(continue)
	if !(asstIdx >= 0 && toolIdx > asstIdx && continueIdx > toolIdx) {
		t.Errorf("message order wrong: asstIdx=%d toolIdx=%d continueIdx=%d", asstIdx, toolIdx, continueIdx)
	}
}

// TestRequestBody_TerminatedToolCall 工具调用被强行终止/结果缺失时的回放：
// assistant 保留 tool_calls，对无对应结果的调用补发占位 role:"tool" 消息
// （说明调用被终止），保证每个 tool_call_id 都有响应，满足 API 校验
// "assistant with tool_calls must be followed by tool messages"。
func TestRequestBody_TerminatedToolCall(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	// 场景 A：工具被强行终止——assistant 带 tool_calls 但结果消息缺失
	msgs := []structs.Messages{
		{ChatID: 10, Type: structs.MessagesRoleUser, Delta: "please call a tool"},
		{ChatID: 10, Type: structs.MessagesRoleAgent, Delta: "calling now", ToolCallingJSONString: `[{"name":"run","id":"call_kill","parameters":{"command":"ls"}}]`},
		{ChatID: 10, Type: structs.MessagesRoleUser, Delta: "never mind"},
	}
	for _, mm := range msgs {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	req, err := RequestBody(10, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	terminatedToolFound := false
	for _, msg := range req.Messages {
		if msg.Role == reqStruct.RoleAssistant {
			if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_kill" {
				t.Errorf("terminated assistant should keep its tool_calls: %+v", msg.ToolCalls)
			}
			if !strings.Contains(msg.Content, "calling now") {
				t.Errorf("assistant text should be kept, got %q", msg.Content)
			}
		}
		if msg.Role == reqStruct.RoleTool && msg.ToolCallID == "call_kill" {
			if !strings.Contains(msg.Content, "terminated") {
				t.Errorf("terminated tool result should mention termination, got %q", msg.Content)
			}
			terminatedToolFound = true
		}
	}
	if !terminatedToolFound {
		t.Error("terminated tool call should emit a placeholder role:tool result")
	}

	// 场景 B：部分结果——assistant 两个调用仅一个结果 → call_a 真实结果、call_b 补终止占位
	msgsB := []structs.Messages{
		{ChatID: 11, Type: structs.MessagesRoleUser, Delta: "two tools"},
		{ChatID: 11, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"run","id":"call_a","parameters":{}},{"name":"edit","id":"call_b","parameters":{}}]`},
		{ChatID: 11, Type: structs.MessagesRoleTool, Delta: `[{"name":"run","id":"call_a","return":"{\"ok\":true}"}]`},
	}
	for _, mm := range msgsB {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg B: %v", err)
		}
	}
	reqB, err := RequestBody(11, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody B failed: %v", err)
	}
	asstBCount := 0
	realA, termB := false, false
	for _, msg := range reqB.Messages {
		if msg.Role == reqStruct.RoleAssistant {
			asstBCount = len(msg.ToolCalls)
		}
		if msg.Role == reqStruct.RoleTool {
			switch msg.ToolCallID {
			case "call_a":
				if strings.Contains(msg.Content, "terminated") {
					t.Error("call_a should carry real result, not termination placeholder")
				}
				realA = true
			case "call_b":
				if !strings.Contains(msg.Content, "terminated") {
					t.Errorf("call_b should carry termination placeholder, got %q", msg.Content)
				}
				termB = true
			}
		}
	}
	if asstBCount != 2 {
		t.Errorf("partial-result assistant should keep 2 tool_calls, got %d", asstBCount)
	}
	if !realA || !termB {
		t.Errorf("partial-result: real result call_a=%v, terminated placeholder call_b=%v", realA, termB)
	}
}

// TestRequestBody_RecentFiveToolTurns 只对最近 5 轮工具调用完整回放：
// 更旧的工具调用轮次（assistant 带 tool_calls）降级为纯文本，其调用与结果均不回放。
// TestRequestBody_AllToolTurnsReplayed 截断点之后所有工具调用轮次完整回放 tool_calls：
// 6 轮历史全部携带 tool_calls 结构、无纯文本降级、结果全部配对回放——同一条消息的
// 渲染不再随轮次推移变化，前缀缓存才不被破坏。
func TestRequestBody_AllToolTurnsReplayed(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	// 6 轮工具调用，按 id 递增（call_1 最旧 → call_6 最新），每轮都带结果
	var msgs []structs.Messages
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs,
			structs.Messages{ChatID: 12, Type: structs.MessagesRoleAgent, Delta: fmt.Sprintf("turn %d", i), ToolCallingJSONString: `[{"name":"run","id":"` + id + `","parameters":{"command":"echo"}}]`},
			structs.Messages{ChatID: 12, Type: structs.MessagesRoleTool, Delta: `[{"name":"run","id":"` + id + `","return":"{\"ok\":true}"}]`},
		)
	}
	for _, mm := range msgs {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	req, err := RequestBody(12, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	asstWithTools := 0 // 携带 tool_calls 的 assistant 数
	asstTextOnly := 0  // 无 tool_calls 的 assistant 数
	toolResults := 0   // role:"tool" 结果数
	hasOldest, hasNewest := false, false
	for _, msg := range req.Messages {
		if msg.Role == reqStruct.RoleAssistant {
			if len(msg.ToolCalls) > 0 {
				asstWithTools++
				if msg.ToolCalls[0].ID == "call_1" {
					hasOldest = true
				}
				if msg.ToolCalls[0].ID == "call_6" {
					hasNewest = true
				}
			} else {
				asstTextOnly++
			}
		}
		if msg.Role == reqStruct.RoleTool {
			toolResults++
		}
	}
	if asstWithTools != 6 {
		t.Errorf("expected all 6 turns with tool_calls, got %d", asstWithTools)
	}
	if asstTextOnly != 0 {
		t.Errorf("expected no text-only downgrade, got %d", asstTextOnly)
	}
	if !hasOldest {
		t.Error("oldest turn (call_1) should carry tool_calls")
	}
	if !hasNewest {
		t.Error("newest turn (call_6) should carry tool_calls")
	}
	if toolResults != 6 {
		t.Errorf("expected all 6 tool results replayed, got %d", toolResults)
	}
}

// helper: 原生模式回放辅助 —— 对给定消息序列调用 RequestBody，
// 返回 assistant 消息中指定 id 的工具调用 arguments。
func replayArguments(t *testing.T, chatID uint32, msgs []structs.Messages, callID string) string {
	t.Helper()
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}
	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)
	for _, mm := range msgs {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	req, err := RequestBody(chatID, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	for _, msg := range req.Messages {
		if msg.Role == reqStruct.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.ID == callID && tc.Function != nil {
					return tc.Function.Arguments
				}
			}
		}
	}
	return ""
}

// TestRequestBody_ShortArgsReplayedReal 历史回放始终是真实 JSON：
// 模型曾以错误参数名（"paht"）调用 edit —— 回放时 arguments 为当初传入的真实 JSON，
// 让模型看到自己传错的参数名（回放不做任何省略）。
func TestRequestBody_ShortArgsReplayedReal(t *testing.T) {
	args := replayArguments(t, 12, []structs.Messages{
		{ChatID: 12, Type: structs.MessagesRoleUser, Delta: "edit a.txt"},
		{ChatID: 12, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_fail","parameters":{"paht":"a.txt","text":"hi"}}]`},
		{ChatID: 12, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_fail","return":"{\"success\":false,\"error\":\"missing path parameter\"}"}]`},
		{ChatID: 12, Type: structs.MessagesRoleUser, Delta: "continue"},
	}, "call_fail")
	if args != `{"paht":"a.txt","text":"hi"}` {
		t.Errorf("failed call should replay real arguments, got %q", args)
	}
}

// TestRequestBody_AllShortArgsReplayed 同一轮内所有短参数（不分成功/失败）均回放真实 JSON：
// 参数统一规则，不再区分调用成败。
func TestRequestBody_AllShortArgsReplayed(t *testing.T) {
	msgs := []structs.Messages{
		{ChatID: 13, Type: structs.MessagesRoleUser, Delta: "two calls"},
		{ChatID: 13, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_ok","parameters":{"path":"a.txt"}},{"name":"edit","id":"call_fail","parameters":{"paht":"b.txt"}}]`},
		{ChatID: 13, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_ok","return":"{\"success\":true}"},{"name":"edit","id":"call_fail","return":"{\"success\":false}"}]`},
	}
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}
	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)
	for _, mm := range msgs {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	req, err := RequestBody(13, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	gotOK, gotFail := "", ""
	for _, msg := range req.Messages {
		if msg.Role == reqStruct.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.Function == nil {
					continue
				}
				switch tc.ID {
				case "call_ok":
					gotOK = tc.Function.Arguments
				case "call_fail":
					gotFail = tc.Function.Arguments
				}
			}
		}
	}
	if gotOK != `{"path":"a.txt"}` {
		t.Errorf("short args should replay real JSON regardless of success, got %q", gotOK)
	}
	if gotFail != `{"paht":"b.txt"}` {
		t.Errorf("short args should replay real JSON regardless of failure, got %q", gotFail)
	}
}

// TestParseStoredToolCalls_ReplaysCompleteArgs 历史回放始终回放完整参数，不做任何省略。
// 回归背景：任何省略占位符都会被模型当成真实取值照抄——
//   - 整包换成 "..." → 模型产出 {"arguments":"..."}，run 报 "type is required"；
//   - 换成 "<omitted: N chars>" → 模型把该字面量写成 edit 的 text，文件被污染。
func TestParseStoredToolCalls_ReplaysCompleteArgs(t *testing.T) {
	longPath := strings.Repeat("a", 201)
	longJSON := `{"path":"` + longPath + `"}`
	payload := `[{"name":"edit","id":"call_long","parameters":` + longJSON + `},{"name":"edit","id":"call_short","parameters":{"path":"b.txt"}},{"name":"edit","id":"call_empty"}]`

	calls, err := parseStoredToolCalls(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if got := calls[0].Function.Arguments; got != longJSON {
		t.Errorf("long args must replay complete, got %q", got)
	}
	if got := calls[1].Function.Arguments; got != `{"path":"b.txt"}` {
		t.Errorf("short args should replay unchanged, got %q", got)
	}
	if got := calls[2].Function.Arguments; got != "{}" {
		t.Errorf("missing params should replay as an empty object, got %q", got)
	}
	for _, c := range calls {
		args := c.Function.Arguments
		if !json.Valid([]byte(args)) {
			t.Errorf("replayed arguments must be valid JSON: %q", args)
		}
		if strings.Contains(args, "omitted") || args == "..." || strings.Contains(args, `"arguments"`) {
			t.Errorf("replay must not contain omission markers or an invented arguments key: %q", args)
		}
	}

	// 超长字段（含中文）同样完整回放：回放层不存在任何长度阈值
	cnLong := strings.Repeat("汉", 5000)
	payload2 := `[{"name":"edit","id":"cn_long","parameters":{"text":"` + cnLong + `"}}]`
	calls2, err := parseStoredToolCalls(payload2)
	if err != nil {
		t.Fatalf("parse cn: %v", err)
	}
	var gotCN map[string]string
	if err := json.Unmarshal([]byte(calls2[0].Function.Arguments), &gotCN); err != nil {
		t.Fatalf("cn args must be valid JSON: %v", err)
	}
	if gotCN["text"] != cnLong {
		t.Errorf("long CJK field must replay complete, got %d bytes want %d", len(gotCN["text"]), len(cnLong))
	}
}

// TestRequestBody_LongArgsReplayedComplete 端到端回归（RequestBody 层）：run 这类长参数
// 调用回放时 type/reason/command 三个参数名一个不少，且每个字段都是完整原值。
// 旧实现把整个 arguments 换成 "..."，模型因此在 chat 458 模仿出 {"arguments":"..."}，
// 触发 "[System] Parameter Error: type is required"；改成字段级 "<omitted: N chars>" 后
// 模型又把占位符当成 text 写进文件，因此现在一律完整回放、不做任何省略。
func TestRequestBody_LongArgsReplayedComplete(t *testing.T) {
	command := strings.Repeat("echo hello; ", 60) // 720 rune，故意远超任何"大字段"直觉阈值
	toolCall := fmt.Sprintf("[{\"name\":\"run\",\"id\":\"call_long\",\"parameters\":{\"type\":\"shell\",\"reason\":\"check build\",\"command\":%q}}]", command)
	args := replayArguments(t, 14, []structs.Messages{
		{ChatID: 14, Type: structs.MessagesRoleUser, Delta: "run it"},
		{ChatID: 14, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: toolCall},
	}, "call_long")

	var got map[string]string
	if err := json.Unmarshal([]byte(args), &got); err != nil {
		t.Fatalf("replayed arguments must be a valid JSON object: %v (%q)", err, args)
	}
	if got["type"] != "shell" || got["reason"] != "check build" || got["command"] != command {
		t.Errorf("every field must replay complete, got %q", args)
	}
	if _, ok := got["arguments"]; ok {
		t.Errorf("replay must not fabricate an 'arguments' parameter: %q", args)
	}
	if strings.Contains(args, "omitted") || strings.Contains(args, "...") {
		t.Errorf("replay must not contain omission markers: %q", args)
	}
}

// TestRequestBody_RebuildOmitsSupersededEdits 方案1（放弃 diff 缓存、注入完整内容）之后：
// 该文件在注入 read 之前的 edit 调用（及其 role:"tool" 结果）不再出现在请求体里；
// 注入点本身的 read 调用与注入的完整内容块保持不动。
func TestRequestBody_RebuildOmitsSupersededEdits(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 40, Type: structs.MessagesRoleUser, Delta: "edit a.txt"},
		{ChatID: 40, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"edit","id":"call_e1","parameters":{"path":"a.txt","target":"@all","text":"NEW CONTENT"}}]`},
		{ChatID: 40, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_e1","return":"{"success":true}"}]`},
		{ChatID: 40, Type: structs.MessagesRoleUser, Delta: "read a.txt"},
		{ChatID: 40, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"read","id":"call_r1","parameters":{"path":"a.txt"}}]`},
		{ChatID: 40, Type: structs.MessagesRoleTool, Delta: `[{"name":"read","id":"call_r1","return":"data"}]`},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	// 最新事件是 read、最早事件是 edit，没有 diff 计划 → 方案1（注入完整内容）。
	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			"a.txt": {MsgID: msgs[4].ID, ToolCallID: "call_r1", IsEdit: false},
		},
		map[string]trace.FileBlock{
			"a.txt": {Name: "a.txt", Size: "11", Length: 11, Text: "NEW CONTENT"},
		},
	)
	chatLn.TemporyDataOfSession[structs.TempKeyTracePrevEvents] = map[string]*structs.TraceEvent{
		"a.txt": {MsgID: msgs[1].ID, ToolCallID: "call_e1", IsEdit: true},
	}

	req, err := RequestBody(40, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	var hasEditCall, hasEditResult, hasReadCall, hasReadResult, hasBlock bool
	for _, msg := range req.Messages {
		if msg.Role == reqStruct.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				switch tc.ID {
				case "call_e1":
					hasEditCall = true
				case "call_r1":
					hasReadCall = true
				}
			}
		}
		if msg.Role == reqStruct.RoleTool {
			switch msg.ToolCallID {
			case "call_e1":
				hasEditResult = true
			case "call_r1":
				hasReadResult = true
			}
		}
		if msg.Role == reqStruct.RoleUser && strings.Contains(msg.Content, `path="a.txt"`) {
			hasBlock = true
		}
	}
	if hasEditCall || hasEditResult {
		t.Errorf("方案1 注入完整内容后，注入点之前的 edit 调用应被省略：call=%v result=%v", hasEditCall, hasEditResult)
	}
	if !hasReadCall || !hasReadResult {
		t.Errorf("注入点本身的 read 调用与结果必须保留：call=%v result=%v", hasReadCall, hasReadResult)
	}
	if !hasBlock {
		t.Error("expected injected full content block for a.txt")
	}

	// 只省略请求体的拼接：数据库行必须原样保留（不删、不改）。
	var row structs.Messages
	if err := db.Where("id = ?", msgs[1].ID).First(&row).Error; err != nil {
		t.Fatalf("reload edit message: %v", err)
	}
	if !strings.Contains(row.ToolCallingJSONString, "call_e1") || !strings.Contains(row.ToolCallingJSONString, "NEW CONTENT") {
		t.Errorf("数据库行不得被请求体省略影响：%q", row.ToolCallingJSONString)
	}

	// 前端展示（session/resume 从落库参数重建）也必须保持完整内容。
	var stored []map[string]any
	if err := json.Unmarshal([]byte(row.ToolCallingJSONString), &stored); err != nil || len(stored) != 1 {
		t.Fatalf("stored tool call unparsable: %v (%q)", err, row.ToolCallingJSONString)
	}
	display := fmt.Sprintf("%v", structs.BuildToolCallingContent("edit", msgs[1].ID, stored[0]["parameters"]))
	if !strings.Contains(display, "NEW CONTENT") || !strings.Contains(display, "a.txt") {
		t.Errorf("前端展示必须保留完整参数：%s", display)
	}
}

// TestRequestBody_KeepDiffPlanRetainsEdits 方案2（保留缓存 + diff）不得省略 edit 调用：
// 只有真正放弃缓存、注入完整内容的路径才做省略。
func TestRequestBody_KeepDiffPlanRetainsEdits(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 41, Type: structs.MessagesRoleUser, Delta: "edit a.txt"},
		{ChatID: 41, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"edit","id":"call_e1","parameters":{"path":"a.txt","target":"@all","text":"NEW CONTENT"}}]`},
		{ChatID: 41, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_e1","return":"{"success":true}"}]`},
		{ChatID: 41, Type: structs.MessagesRoleUser, Delta: "read a.txt"},
		{ChatID: 41, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"read","id":"call_r1","parameters":{"path":"a.txt"}}]`},
		{ChatID: 41, Type: structs.MessagesRoleTool, Delta: `[{"name":"read","id":"call_r1","return":"data"}]`},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			"a.txt": {MsgID: msgs[4].ID, ToolCallID: "call_r1", IsEdit: false},
		},
		map[string]trace.FileBlock{
			"a.txt": {Name: "a.txt", Size: "11", Length: 11, Text: "NEW CONTENT"},
		},
	)
	chatLn.TemporyDataOfSession[structs.TempKeyTracePrevEvents] = map[string]*structs.TraceEvent{
		"a.txt": {MsgID: msgs[1].ID, ToolCallID: "call_e1", IsEdit: true},
	}
	// 方案2：保留旧块 + diff（此处 token 计数为 0，软条件必然成立）。
	chatLn.TemporyDataOfSession[structs.TempKeyTraceDiffPlan] = map[string]trace.DiffPlan{
		"a.txt": {
			Keep:      true,
			OldBlock:  trace.FileBlock{Name: "a.txt", Size: "3", Length: 3, Text: "OLD"},
			DiffBlock: trace.FileBlock{Name: "a.txt", Size: "9", Length: 9, Text: "-OLD+NEW", Type: "diff"},
		},
	}

	req, err := RequestBody(41, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	hasEditCall, hasEditResult := false, false
	for _, msg := range req.Messages {
		if msg.Role == reqStruct.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "call_e1" {
					hasEditCall = true
				}
			}
		}
		if msg.Role == reqStruct.RoleTool && msg.ToolCallID == "call_e1" {
			hasEditResult = true
		}
	}
	if !hasEditCall || !hasEditResult {
		t.Errorf("方案2 保留缓存时不应省略 edit 调用：call=%v result=%v", hasEditCall, hasEditResult)
	}
}

// eventTestChatLn 构造带事件映射与内容块的 chatLn（模拟 DetectTraceEvents + PreHook 分区的结果）。
func eventTestChatLn(events map[string]*structs.TraceEvent, fileBlocks map[string]trace.FileBlock) *structs.Chats {
	return &structs.Chats{
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents:     events,
			structs.TempKeyTraceFileBlocks: fileBlocks,
		},
	}
}

// TestRequestBody_OldTurnEventBlockAfterResults 旧轮次（InRecent=false）的事件内容块
// 也必须锚定到该轮 assistant 之后的所有 role:tool 结果之后，而非 assistant 与结果之间。
// 去掉 findEventAnchor 的 InRecent 门控后，全部工具调用轮次均完整回放 tool_calls。
func TestRequestBody_OldTurnEventBlockAfterResults(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	// 6 轮 read 工具调用，事件指向最旧一轮 call_1（模拟 DetectTraceEvents 只给最近 5 轮
	// 标 InRecent=true，故 call_1 为 InRecent=false）
	var msgs []structs.Messages
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs,
			structs.Messages{ChatID: 30, Type: structs.MessagesRoleUser, Delta: fmt.Sprintf("turn %d", i)},
			structs.Messages{ChatID: 30, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"read","id":"` + id + `","parameters":{"path":"a.txt"}}]`},
			structs.Messages{ChatID: 30, Type: structs.MessagesRoleTool, Delta: `[{"name":"read","id":"` + id + `","return":"data"}]`},
		)
	}
	for _, mm := range msgs {
		if err := db.Create(&mm).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	var call1Asst structs.Messages
	if err := db.Where("chat_id = 30 AND type = ?", structs.MessagesRoleAgent).Order("id ASC").First(&call1Asst).Error; err != nil {
		t.Fatalf("find call_1 assistant: %v", err)
	}

	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			"a.txt": {MsgID: call1Asst.ID, ToolCallID: "call_1", IsEdit: false, InRecent: false},
		},
		map[string]trace.FileBlock{
			"a.txt": {Name: "a.txt", Size: "4", Length: 4, Text: "1|data\n"},
		},
	)

	req, err := RequestBody(30, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	call1AsstIdx, call1ResultIdx, blockIdx := -1, -1, -1
	for i, msg := range req.Messages {
		if msg.Role == reqStruct.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				if tc.ID == "call_1" {
					call1AsstIdx = i
				}
			}
		}
		if msg.Role == reqStruct.RoleTool && msg.ToolCallID == "call_1" {
			call1ResultIdx = i
		}
		if msg.Role == "user" && strings.Contains(msg.Content, `path="a.txt"`) {
			blockIdx = i
		}
	}
	if call1AsstIdx < 0 {
		t.Fatal("expected call_1 assistant message")
	}
	if call1ResultIdx < 0 {
		t.Fatal("expected call_1 tool result")
	}
	if blockIdx < 0 {
		t.Fatal("expected trace content block")
	}
	if !(blockIdx > call1ResultIdx) {
		t.Errorf("old-turn content block must come AFTER call_1 tool result (not between assistant and result): asstIdx=%d resultIdx=%d blockIdx=%d", call1AsstIdx, call1ResultIdx, blockIdx)
	}
}

func TestRequestBody_DiffPlanFallbackAdvancesCache(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&structs.Traces{}); err != nil {
		t.Fatalf("migrate traces: %v", err)
	}
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 31, Type: structs.MessagesRoleUser, Delta: "read a.txt"},
		{ChatID: 31, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"read","id":"call_1","parameters":{"path":"a.txt"}}]`},
		{ChatID: 31, Type: structs.MessagesRoleTool, Delta: `[{"name":"read","id":"call_1","return":"data"}]`},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}
	if err := db.Create(&structs.Traces{ChatID: 31, Path: "a.txt", AgentID: "agent", LastContent: "old\n"}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}

	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			// 没有 prev event，方案2 无法插入稳定旧块，必须退化成完整块。
			"a.txt": {MsgID: msgs[1].ID, ToolCallID: "call_1", IsEdit: false},
		},
		map[string]trace.FileBlock{
			"a.txt": {Name: "a.txt", Size: "4", Length: 4, Text: "1|new\n"},
		},
	)
	chatLn.ID = 31
	chatLn.DB = db
	chatLn.NowAgent = "agent"
	chatLn.TemporyDataOfSession[structs.TempKeyTraceDiffPlan] = map[string]trace.DiffPlan{
		"a.txt": {Keep: true},
	}
	// RenderTraceBlocks 已在本轮确认当前磁盘内容；请求构建退化时据此推进完整块基线。
	trace.ConfirmEditContent(chatLn, "a.txt", "new\n")

	req, err := RequestBody(31, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	if !strings.Contains(messagesContent(req.Messages), "1|new") {
		t.Fatal("fallback should inject the current full file block")
	}
	var got structs.Traces
	if err := db.Where("chat_id = 31 AND path = 'a.txt'").First(&got).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if got.LastContent != "new\n" {
		t.Fatalf("fallback must advance LastContent to full block content, got %q", got.LastContent)
	}
}

// TestRequestBody_OmittedAllToolCallsNoEmptyArray 回归实战发现的问题：
// 某个 assistant 消息的工具调用被 omitSupersededEditCalls 全部省略、但消息本身带 thinking
// 而保留时，绝不能再序列化出 "tool_calls":[]（DeepSeek 400：
// Invalid 'messages[N].tool_calls': empty array）。
func TestRequestBody_OmittedAllToolCallsNoEmptyArray(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	// "edit 创建文件 → read 确认"：最新事件是 read，唯一 edit 调用会被省略；消息带 thinking。
	msgs := []structs.Messages{
		{ChatID: 47, Type: structs.MessagesRoleUser, Delta: "创建并确认"},
		{ChatID: 47, Type: structs.MessagesRoleAgent, ThinkingDelta: "我先创建文件，然后读一遍确认。",
			ToolCallingJSONString: `[{"name":"edit","id":"call_e1","parameters":{"path":"E2E_NOTES.md","target":"@all","text":"notes"}}]`},
		{ChatID: 47, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_e1","return":"{\"success\":true}"}]`},
		{ChatID: 47, Type: structs.MessagesRoleAgent, ThinkingDelta: "读一遍确认。",
			ToolCallingJSONString: `[{"name":"read","id":"call_r1","parameters":{"path":"E2E_NOTES.md"}}]`},
		{ChatID: 47, Type: structs.MessagesRoleTool, Delta: `[{"name":"read","id":"call_r1","return":"notes"}]`},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			"E2E_NOTES.md": {MsgID: msgs[3].ID, ToolCallID: "call_r1", IsEdit: false},
		},
		map[string]trace.FileBlock{
			"E2E_NOTES.md": {Name: "E2E_NOTES.md", Size: "5", Length: 5, Text: "notes"},
		},
	)
	chatLn.TemporyDataOfSession[structs.TempKeyTracePrevEvents] = map[string]*structs.TraceEvent{
		"E2E_NOTES.md": {MsgID: msgs[1].ID, ToolCallID: "call_e1", IsEdit: true},
	}

	req, err := RequestBody(47, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"tool_calls":[]`) {
		t.Fatalf("省略光工具调用后不得输出空数组 tool_calls，序列化结果含 \"tool_calls\":[]")
	}
	// 带 thinking 的消息本身应保留（省略的是调用，不是整轮思考）
	foundReasoning := false
	for _, msg := range req.Messages {
		if msg.Role == reqStruct.RoleAssistant && msg.ReasoningContent != nil && *msg.ReasoningContent == "我先创建文件，然后读一遍确认。" {
			foundReasoning = true
			if len(msg.ToolCalls) != 0 {
				t.Errorf("被省略的调用不应残留: %+v", msg.ToolCalls)
			}
		}
	}
	if !foundReasoning {
		t.Error("带 thinking 的 assistant 消息应保留（只省略工具调用）")
	}
}

func messagesContent(messages []reqStruct.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(message.Content)
	}
	return builder.String()
}

// （assistant 调用 + 其 role:tool 结果）之后，事件之前不出现该内容。
func TestRequestBody_TraceFollowsEvent_Native(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 20, Type: structs.MessagesRoleUser, Delta: "edit a.txt"},
		{ChatID: 20, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_1","parameters":{"path":"a.txt","text":"hi"}}]`},
		{ChatID: 20, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_1","return":"{\"ok\":true}"}]`},
		{ChatID: 20, Type: structs.MessagesRoleUser, Delta: "continue"},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	asstID := msgs[1].ID

	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			"a.txt": {MsgID: asstID, ToolCallID: "call_1", IsEdit: true, InRecent: true},
		},
		map[string]trace.FileBlock{
			"a.txt": {Name: "a.txt", Size: "5", Length: 5, Text: "1|hi\n"},
		},
	)

	req, err := RequestBody(20, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	toolIdx, blockIdx := -1, -1
	for i, msg := range req.Messages {
		if msg.Role == reqStruct.RoleTool && msg.ToolCallID == "call_1" {
			toolIdx = i
		}
		if msg.Role == "user" && strings.Contains(msg.Content, `path="a.txt"`) {
			blockIdx = i
		}
	}
	if toolIdx < 0 {
		t.Fatal("expected tool result for call_1")
	}
	if blockIdx < 0 {
		t.Fatal("expected trace content block after event")
	}
	if !(blockIdx > toolIdx) {
		t.Errorf("content block should follow tool result: toolIdx=%d blockIdx=%d", toolIdx, blockIdx)
	}
	// 事件之前不应出现该文件内容块
	for i := 0; i < toolIdx; i++ {
		if strings.Contains(req.Messages[i].Content, `path="a.txt"`) {
			t.Errorf("event file content should NOT appear before event, idx=%d", i)
		}
	}
}

// TestRequestBody_ParallelToolCalls_BlockAfterAllResults 同一 assistant 并行输出多个 edit
// tool_calls（一条消息两个 tool_use、两条 tool_result）时，trace 内容块必须插在「所有」
// role:tool 结果之后，不得夹在结果序列中间。否则后续 tool_result 与其 tool_use 被内容块隔开，
// OpenAI→Anthropic 代理会 400（tool_result must have a corresponding tool_use in previous message）。
func TestRequestBody_ParallelToolCalls_BlockAfterAllResults(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 25, Type: structs.MessagesRoleUser, Delta: "edit a.txt twice"},
		{ChatID: 25, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_1","parameters":{"path":"a.txt","text":"x"}},{"name":"edit","id":"call_2","parameters":{"path":"a.txt","text":"y"}}]`},
		{ChatID: 25, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_1","return":"{\"ok\":true}"}]`},
		{ChatID: 25, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_2","return":"{\"ok\":true}"}]`},
		{ChatID: 25, Type: structs.MessagesRoleUser, Delta: "continue"},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	asstID := msgs[1].ID

	// 事件：a.txt 最新 edit 事件（DetectTraceEvents 记录同条 assistant 首个 tool_call call_1）。
	// 修复前 findEventAnchor 只匹配 ToolCallID==call_1 的结果 → 内容块插在 call_1 与 call_2 结果之间；
	// 修复后锚定该 assistant 之后最后一个 role:tool 结果 → 内容块位于全部结果之后。
	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			"a.txt": {MsgID: asstID, ToolCallID: "call_1", IsEdit: true, InRecent: true},
		},
		map[string]trace.FileBlock{
			"a.txt": {Name: "a.txt", Size: "5", Length: 5, Text: "1|hi\n"},
		},
	)

	req, err := RequestBody(25, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	lastToolIdx, blockIdx := -1, -1
	for i, msg := range req.Messages {
		if msg.Role == reqStruct.RoleTool {
			lastToolIdx = i
		}
		if msg.Role == "user" && strings.Contains(msg.Content, `path="a.txt"`) {
			blockIdx = i
		}
	}
	if lastToolIdx < 0 {
		t.Fatal("expected tool result messages")
	}
	if blockIdx < 0 {
		t.Fatal("expected trace content block after event")
	}
	if !(blockIdx > lastToolIdx) {
		t.Errorf("content block must come AFTER all tool results (not between them): lastToolIdx=%d blockIdx=%d", lastToolIdx, blockIdx)
	}
}

// TestRequestBody_TraceFollowsEvent_MultiFileOneMessage 同一事件内多文件合并为一条 user 消息，
// 且位于该事件所有 tool 结果之后。
func TestRequestBody_TraceFollowsEvent_MultiFileOneMessage(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 22, Type: structs.MessagesRoleUser, Delta: "edit two files"},
		{ChatID: 22, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_1","parameters":{"path":"a.txt"}},{"name":"edit","id":"call_2","parameters":{"path":"b.txt"}}]`},
		{ChatID: 22, Type: structs.MessagesRoleTool, Delta: `[{"name":"edit","id":"call_1","return":"{\"ok\":true}"},{"name":"edit","id":"call_2","return":"{\"ok\":true}"}]`},
		{ChatID: 22, Type: structs.MessagesRoleUser, Delta: "continue"},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	asstID := msgs[1].ID

	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			"a.txt": {MsgID: asstID, ToolCallID: "call_1", IsEdit: true, InRecent: true},
			"b.txt": {MsgID: asstID, ToolCallID: "call_2", IsEdit: true, InRecent: true},
		},
		map[string]trace.FileBlock{
			"a.txt": {Name: "a.txt", Size: "5", Length: 5, Text: "1|AAA\n"},
			"b.txt": {Name: "b.txt", Size: "5", Length: 5, Text: "1|BBB\n"},
		},
	)

	req, err := RequestBody(22, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	// 找到 call_1 / call_2 两个 tool 结果的最大 index，以及合并内容块的 index
	lastToolIdx, blockIdx, blockCount := -1, -1, 0
	for i, msg := range req.Messages {
		if msg.Role == reqStruct.RoleTool && (msg.ToolCallID == "call_1" || msg.ToolCallID == "call_2") {
			if i > lastToolIdx {
				lastToolIdx = i
			}
		}
		if msg.Role == "user" && strings.Contains(msg.Content, `path="a.txt"`) && strings.Contains(msg.Content, `path="b.txt"`) {
			blockIdx = i
			blockCount++
		}
	}
	if blockCount != 1 {
		t.Errorf("expected exactly one merged content block, got %d", blockCount)
	}
	if blockIdx < 0 {
		t.Fatal("expected merged content block with both files")
	}
	if !(blockIdx > lastToolIdx) {
		t.Errorf("merged block should follow all tool results: lastToolIdx=%d blockIdx=%d", lastToolIdx, blockIdx)
	}
}

// TestRequestBody_MissingAnchor_FallsBackTail 锚点不可渲染（事件消息被 skip / 超出回放窗口）时，
// 内容块回退到**消息列表末尾**，而不是"历史之前的顶部"：顶部 fallback 会把块放到整个对话之前，
// 块一变后面全部历史都失去前缀缓存（docs/trace-cache-spec.md §4.2）。
func TestRequestBody_MissingAnchor_FallsBackTail(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 23, Type: structs.MessagesRoleUser, Delta: "go"},
		{ChatID: 23, Type: structs.MessagesRoleAgent, Delta: "working", ToolCallingJSONString: `[{"name":"read","id":"call_1","parameters":{"path":"a.txt"}}]`},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	// 事件 MsgID 指向不存在的记录 → findEventAnchor 返回 nil → 末尾 fallback
	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{
			"a.txt": {MsgID: 99999, ToolCallID: "call_1", IsEdit: false, InRecent: true},
		},
		map[string]trace.FileBlock{
			"a.txt": {Name: "a.txt", Size: "5", Length: 5, Text: "1|hi\n"},
		},
	)

	req, err := RequestBody(23, 1, "", &toolsList, db, "", "TOP_MARKER", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	topIdx, blockIdx := -1, -1
	for i, msg := range req.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "TOP_MARKER") {
			topIdx = i
		}
		if msg.Role == "user" && strings.Contains(msg.Content, `path="a.txt"`) {
			blockIdx = i
		}
	}
	if topIdx < 0 {
		t.Fatal("expected addUserPrompt top marker")
	}
	if blockIdx < 0 {
		t.Fatal("expected fallback content block")
	}
	if blockIdx != len(req.Messages)-1 {
		t.Errorf("fallback block should be appended at the tail: blockIdx=%d total=%d", blockIdx, len(req.Messages))
	}
	if blockIdx < topIdx {
		t.Errorf("fallback block must not precede the global prehook block: topIdx=%d blockIdx=%d", topIdx, blockIdx)
	}
}

// TestRequestBody_NoEventFile_Top 无事件映射时，trace 内容仍在顶部聚合（现状），历史中无内容块。
func TestRequestBody_NoEventFile_Top(t *testing.T) {
	db := setupTestDB(t)
	toolsList := []*parser.ToolsDefine{}

	setupTestConfig()
	cfg := *config.GlobalConfig
	m := cfg.Model.Models[cfg.Model.DefaultModelID]
	cfg.Model.Models[cfg.Model.DefaultModelID] = m
	config.GlobalConfigSwap(cfg)

	msgs := []structs.Messages{
		{ChatID: 24, Type: structs.MessagesRoleUser, Delta: "hello"},
	}
	if err := db.Create(&msgs[0]).Error; err != nil {
		t.Fatalf("create msg: %v", err)
	}

	// 无 TemporyDataOfSession → 无事件 → 内容全在顶部 addUserPrompt
	req, err := RequestBody(24, 1, "", &toolsList, db, "", "TOP a.txt trace block", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	topIdx, histIdx := -1, -1
	for i, msg := range req.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "TOP a.txt") {
			topIdx = i
		}
		if msg.Role == "user" && strings.Contains(msg.Content, "hello") {
			histIdx = i
		}
	}
	if topIdx < 0 {
		t.Fatal("expected top trace block")
	}
	if histIdx < 0 {
		t.Fatal("expected history user message")
	}
	if !(topIdx < histIdx) {
		t.Errorf("top trace block should come before history: topIdx=%d histIdx=%d", topIdx, histIdx)
	}
}

func TestCollectTracePathsAfter(t *testing.T) {
	db := setupTestDB(t)
	msgs := []structs.Messages{
		{ChatID: 40, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"edit","parameters":{"path":"old.txt"}}]`},
		{ChatID: 40, Type: structs.MessagesRoleAgent, AgentID: new("child"), ToolCallingJSONString: `[{"name":"read","parameters":{"path":"child.txt"}}]`},
		{ChatID: 40, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"read","parameters":{"path":"new.txt"}},{"name":"edit","parameters":{"path":"@temp/run"}},{"name":"edit","parameters":{"path":"@task"}}]`},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	paths, err := CollectTracePathsAfter(db, 40, "", msgs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"new.txt", "@temp/run"} {
		if _, ok := paths[path]; !ok {
			t.Errorf("expected %q to be retained", path)
		}
	}
	for _, path := range []string{"old.txt", "child.txt", "@task"} {
		if _, ok := paths[path]; ok {
			t.Errorf("did not expect %q to be retained", path)
		}
	}
}

//go:fix inline
func stringPtr(value string) *string { return new(value) }

func TestDetectTraceEvents(t *testing.T) {
	db := setupTestDB(t)

	// 消息按 id 递增（旧→新）：a.txt 有 3 次事件（最新 read call_1、次新 edit call_2、最早 edit call_0）。
	// prevMap 应记录「最早事件」（call_0）而非次新（call_2）——旧块锚定最早位置，连续编辑才不漂移。
	msgs := []structs.Messages{
		{ChatID: 30, Type: structs.MessagesRoleUser, Delta: "u1"},
		{ChatID: 30, Type: structs.MessagesRoleAgent, Delta: "", Summary: "sum", ToolCallingJSONString: `[{"name":"edit","id":"call_9","parameters":{"path":"z.txt"}}]`},
		{ChatID: 30, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_3","parameters":{"path":"@task"}}]`},
		{ChatID: 30, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_0","parameters":{"path":"a.txt"}}]`},
		{ChatID: 30, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"edit","id":"call_2","parameters":{"path":"a.txt"}}]`},
		{ChatID: 30, Type: structs.MessagesRoleAgent, Delta: "", ToolCallingJSONString: `[{"name":"read","id":"call_1","parameters":{"path":"a.txt"}}]`},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	session := &structs.Chats{ID: 30, DB: db}
	if err := DetectTraceEvents(db, session, ""); err != nil {
		t.Fatalf("DetectTraceEvents: %v", err)
	}
	em, ok := session.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent)
	if !ok {
		t.Fatal("expected trace events in session")
	}

	// a.txt：从新到旧先扫到 read（call_1，最新）
	evA, ok := em["a.txt"]
	if !ok {
		t.Fatal("expected a.txt event")
	}
	if evA.ToolCallID != "call_1" || evA.IsEdit {
		t.Errorf("a.txt latest event should be read call_1, got %+v", evA)
	}
	if evA.MsgID != msgs[5].ID {
		t.Errorf("a.txt event MsgID should be %d, got %d", msgs[5].ID, evA.MsgID)
	}
	// @task 识别
	taskEv, ok := em["@task"]
	if !ok || !taskEv.IsTask || !taskEv.IsEdit {
		t.Errorf("expected @task edit event, got %+v", taskEv)
	}
	// Summary 截断：z.txt 在 summary 之后（更旧），不应被记录
	if _, ok := em["z.txt"]; ok {
		t.Error("z.txt event should be truncated by summary")
	}

	// 最早事件：a.txt 应记录最早 edit call_0（方案2 旧块固定锚点），而非次新 call_2
	pm, ok := session.TemporyDataOfSession[structs.TempKeyTracePrevEvents].(map[string]*structs.TraceEvent)
	if !ok {
		t.Fatal("expected prev events in session")
	}
	prevA, ok := pm["a.txt"]
	if !ok {
		t.Fatal("expected a.txt prev event")
	}
	if prevA.ToolCallID != "call_0" || !prevA.IsEdit {
		t.Errorf("a.txt prev event should be earliest edit call_0, got %+v", prevA)
	}
	if prevA.MsgID != msgs[3].ID {
		t.Errorf("a.txt prev event MsgID should be %d, got %d", msgs[3].ID, prevA.MsgID)
	}
	// @task 只有一次事件，无最早/次新
	if _, ok := pm["@task"]; ok {
		t.Error("@task should have no prev event")
	}
}
