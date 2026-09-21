package build

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// swapP2Config 用指定 temperature/TokenLimit 的模型 1 替换全局配置（测试结束自动还原）。
func swapP2Config(t *testing.T, temp float32, tokenLimit int32, enableTemp bool) {
	t.Helper()
	restore := config.GlobalConfigSwap(cfgStruct.Config{
		Model: cfgStruct.ModelsConfig{
			DefaultModelID: 1,
			Models: map[int32]cfgStruct.ModelConfig{
				1: {
					ModelName:        "p2-model",
					ModelID:          "p2-model-id",
					ModelTemperature: temp,
					TokenLimit:       tokenLimit,
					ProviderSpecificConfig: cfgStruct.ProviderSpecificConfig{
						EnableTemperature: enableTemp,
					},
				},
			},
		},
		Agent: cfgStruct.AgentsConfig{
			SummaryModel: 1,
			TitleModel:   1,
			GlobalPrompt: "You are a helpful assistant",
		},
	})
	t.Cleanup(restore)
}

// emptyMessageDB 返回一个未迁移任何表的数据库（任何读表都会报错）。
func emptyMessageDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open empty db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestBuild_KeepsSystemPromptForRetry 复现 P2-1（build 侧）：Build 只读取、不消费
// session.SystemPrompt——请求失败后调用方重试会再次调用 Build，通知不能丢。
func TestBuild_KeepsSystemPromptForRetry(t *testing.T) {
	db := setupBuildTest(t)
	if err := db.Create(&structs.Chats{ID: 42, LastModelID: 1}).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	session := &structs.Chats{
		ID:           42,
		DB:           db,
		LastModelID:  1,
		InTestFlag:   true,
		SystemPrompt: "[System] queued-notice-42",
	}

	first, err := Build(db, session)
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	second, err := Build(db, session)
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	contains := func(req *reqStruct.ChatCompletionRequest) bool {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "queued-notice-42") {
				return true
			}
		}
		return false
	}
	if !contains(first) {
		t.Error("首次构建丢失排队的 system 通知")
	}
	if !contains(second) {
		t.Error("重试构建丢失排队的 system 通知（Build 消费了 session.SystemPrompt）")
	}
}

// TestRequestBody_MaxTokensRespectsModelTokenLimit 复现 P2-7：
// max_tokens 不能硬编码 16384，模型配置的 TokenLimit 必须生效（小上下文模型收敛）。
func TestRequestBody_MaxTokensRespectsModelTokenLimit(t *testing.T) {
	db := setupTestDB(t)
	buildReq := func() *reqStruct.ChatCompletionRequest {
		t.Helper()
		req, err := RequestBody(1, 1, "", nil, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{ID: 1})
		if err != nil {
			t.Fatalf("RequestBody: %v", err)
		}
		return req
	}

	swapP2Config(t, -1, 4096, false)
	if req := buildReq(); req.MaxTokens == nil || *req.MaxTokens != 4096 {
		t.Errorf("TokenLimit=4096 时 max_tokens 应为 4096，实际 %v（硬编码 16384）", req.MaxTokens)
	}

	swapP2Config(t, -1, 0, false)
	if req := buildReq(); req.MaxTokens == nil || *req.MaxTokens != maxToken {
		t.Errorf("TokenLimit 未配置时应保持默认 %d，实际 %v", maxToken, req.MaxTokens)
	}

	swapP2Config(t, -1, 200000, false)
	if req := buildReq(); req.MaxTokens == nil || *req.MaxTokens != maxToken {
		t.Errorf("TokenLimit 大于默认上限时应保持默认 %d，实际 %v", maxToken, req.MaxTokens)
	}
}

// TestRequestBody_TemperatureZeroIsExplicit 复现 P2-8：temperature=0 是显式值，不能当作未设置。
func TestRequestBody_TemperatureZeroIsExplicit(t *testing.T) {
	swapP2Config(t, 0, 0, true)
	db := setupTestDB(t)

	req, err := RequestBody(1, 1, "", nil, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{ID: 1})
	if err != nil {
		t.Fatalf("RequestBody: %v", err)
	}
	if req.Temperature == nil {
		t.Fatal("temperature=0 被当作未设置（EnableTemperature=true 时应下发显式 0）")
	}
	if *req.Temperature != 0 {
		t.Errorf("temperature 应为 0，实际 %v", *req.Temperature)
	}
}

// TestSummary_TemperatureZeroIsExplicit 复现 P2-8（summary 路径）。
func TestSummary_TemperatureZeroIsExplicit(t *testing.T) {
	swapP2Config(t, 0, 0, true)
	db := setupTestDB(t)
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "hello"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "world"}).Error; err != nil {
		t.Fatalf("create agent msg: %v", err)
	}

	_, req, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if req == nil {
		t.Fatal("Summary 返回空请求")
	}
	if req.Temperature == nil {
		t.Fatal("summary 路径 temperature=0 被当作未设置")
	}
	if *req.Temperature != 0 {
		t.Errorf("temperature 应为 0，实际 %v", *req.Temperature)
	}
}

// TestTitle_TemperatureZeroIsExplicit 复现 P2-8（title 路径）。
func TestTitle_TemperatureZeroIsExplicit(t *testing.T) {
	swapP2Config(t, 0, 0, true)
	db := setupTestDB(t)
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "hello"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "world"}).Error; err != nil {
		t.Fatalf("create agent msg: %v", err)
	}

	req, err := Title(1, db)
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if req == nil {
		t.Fatal("Title 返回空请求（缺少消息？）")
	}
	if req.Temperature == nil {
		t.Fatal("title 路径 temperature=0 被当作未设置")
	}
	if *req.Temperature != 0 {
		t.Errorf("temperature 应为 0，实际 %v", *req.Temperature)
	}
}

// TestRequestBody_ToolCallOnlyAssistantOmitsEmptyContent 复现 P2-9：
// 纯工具调用轮（无正文）的历史回放不得发送 "content":""。
func TestRequestBody_ToolCallOnlyAssistantOmitsEmptyContent(t *testing.T) {
	swapP2Config(t, 0.7, 0, true)
	db := setupTestDB(t)

	if err := db.Create(&structs.Messages{ChatID: 7, Type: structs.MessagesRoleUser, Delta: "please read"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	if err := db.Create(&structs.Messages{
		ChatID:                7,
		Type:                  structs.MessagesRoleAgent,
		Delta:                 "",
		ToolCallingJSONString: `[{"name":"read","id":"call_1","parameters":{"path":"README.md"}}]`,
	}).Error; err != nil {
		t.Fatalf("create assistant msg: %v", err)
	}
	if err := db.Create(&structs.Messages{
		ChatID: 7,
		Type:   structs.MessagesRoleTool,
		Delta:  `[{"name":"read","id":"call_1","return":"file content"}]`,
	}).Error; err != nil {
		t.Fatalf("create tool msg: %v", err)
	}

	req, err := RequestBody(7, 1, "", nil, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{ID: 7})
	if err != nil {
		t.Fatalf("RequestBody: %v", err)
	}

	foundToolCallOnly := false
	userHasContent := false
	for _, m := range req.Messages {
		if m.Role == reqStruct.RoleUser && strings.Contains(m.Content, "please read") {
			userHasContent = true
		}
		if m.Role != reqStruct.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		foundToolCallOnly = true
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal assistant message: %v", err)
		}
		if strings.Contains(string(b), `"content"`) {
			t.Errorf("纯工具调用 assistant 回放仍发送空 content: %s", b)
		}
	}
	if !foundToolCallOnly {
		t.Fatal("未回放到带 tool_calls 的 assistant 消息，用例无效")
	}
	if !userHasContent {
		t.Error("普通 user 消息内容丢失")
	}
}

// TestRequestBody_PropagatesDBError 复现 P2-10：读历史失败必须返回错误，
// 而不是静默降级成"空历史"再发请求。
func TestRequestBody_PropagatesDBError(t *testing.T) {
	swapP2Config(t, 0.7, 0, true)
	db := emptyMessageDB(t)

	if _, err := RequestBody(1, 1, "", nil, db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{ID: 1}); err == nil {
		t.Fatal("读历史失败被丢弃：RequestBody 未返回错误（历史静默为空）")
	}
}

// TestDetectTraceEvents_PropagatesDBError 复现 P2-10（trace 扫描同样吞掉 DB 错误）。
func TestDetectTraceEvents_PropagatesDBError(t *testing.T) {
	db := emptyMessageDB(t)
	if err := DetectTraceEvents(db, &structs.Chats{ID: 1}, ""); err == nil {
		t.Fatal("DetectTraceEvents 吞掉了 DB 错误")
	}
}

// TestSummary_PropagatesDBError 复现 P2-10（summary 读历史的错误被丢弃）。
func TestSummary_PropagatesDBError(t *testing.T) {
	swapP2Config(t, 0.7, 0, true)
	db := emptyMessageDB(t)

	if _, _, err := Summary(1, "", db); err == nil {
		t.Fatal("Summary 读历史失败被丢弃（静默返回空摘要请求）")
	}
}

// TestSummary_SkipsEmptyAssistantMessage 复现 P2-4（回放侧）：
// 空的 assistant 行（取消遗留）不应进入 summary 上下文。
func TestSummary_SkipsEmptyAssistantMessage(t *testing.T) {
	swapP2Config(t, 0.7, 0, true)
	db := setupTestDB(t)

	for _, m := range []structs.Messages{
		{ChatID: 5, Type: structs.MessagesRoleUser, Delta: "first question"},
		{ChatID: 5, Type: structs.MessagesRoleAgent, Delta: ""},
		{ChatID: 5, Type: structs.MessagesRoleUser, Delta: "second question"},
	} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	_, req, err := Summary(5, "", db)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if req == nil {
		t.Fatal("Summary 返回空请求")
	}
	userCount := 0
	for _, m := range req.Messages {
		if m.Role == reqStruct.RoleAssistant && m.Content == "" && m.ReasoningContent == nil {
			t.Errorf("summary 回放了空 assistant 消息: %+v", m)
		}
		if m.Role == reqStruct.RoleUser &&
			(strings.Contains(m.Content, "first question") || strings.Contains(m.Content, "second question")) {
			userCount++
		}
	}
	if userCount != 2 {
		t.Errorf("summary 上下文应有 2 条用户消息，实际 %d 条", userCount)
	}
}
