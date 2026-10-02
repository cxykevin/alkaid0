package build

// 显式缓存开关（prompt_cache_key / cache_control 断点）的结构断言。
// 纯本地断言，不需要任何真实 provider：只验证开关打开/关闭时请求体里"有没有、在哪一条"。

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/parser"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// enablePromptCacheSwitches 在测试配置基础上打开/关闭显式缓存开关。
func enablePromptCacheSwitches(t *testing.T, cacheKey, breakpoints bool) {
	t.Helper()
	setupTestConfig()
	cfg := *config.GlobalConfig
	models := make(map[int32]cfgStruct.ModelConfig, len(cfg.Model.Models))
	for k, v := range cfg.Model.Models {
		models[k] = v
	}
	model := models[cfg.Model.DefaultModelID]
	model.ProviderSpecificConfig.EnablePromptCacheKey = cacheKey
	model.ProviderSpecificConfig.EnablePromptCacheBreakpoints = breakpoints
	models[cfg.Model.DefaultModelID] = model
	cfg.Model.Models = models
	config.GlobalConfigSwap(cfg)
}

func createPromptCacheMessages(t *testing.T, db *gorm.DB, msgs []structs.Messages) {
	t.Helper()
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
}

func noTools() *[]*parser.ToolsDefine { return &[]*parser.ToolsDefine{} }

// TestPromptCacheSwitchesOffByDefault 开关关闭（默认）时请求体里不得出现任何缓存字段。
func TestPromptCacheSwitchesOffByDefault(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)
	createPromptCacheMessages(t, db, []structs.Messages{
		{ChatID: 60, Type: structs.MessagesRoleUser, Delta: "hello"},
	})

	req, err := RequestBody(60, 1, "", noTools(), db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	if req.PromptCacheKey != "" {
		t.Errorf("开关关闭时不应带 prompt_cache_key，got %q", req.PromptCacheKey)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "prompt_cache_key") {
		t.Error("开关关闭时请求体不应含 prompt_cache_key 字段")
	}
	if strings.Contains(string(raw), "cache_control") {
		t.Error("开关关闭时请求体不应含 cache_control")
	}
	for i, m := range req.Messages {
		if m.CacheControl != nil {
			t.Errorf("开关关闭时消息 %d 不应挂断点", i)
		}
	}
}

// TestPromptCacheKeyValueIncludesModelAndAgent 路由键 = 会话 + 模型 +（可选）子代理。
func TestPromptCacheKeyValueIncludesModelAndAgent(t *testing.T) {
	if got := promptCacheKeyValue(7, 1, ""); got != "alkaid0-chat-7-m1" {
		t.Errorf("unexpected key: %q", got)
	}
	if got := promptCacheKeyValue(7, 2, "explore"); got != "alkaid0-chat-7-m2-explore" {
		t.Errorf("unexpected key with agent: %q", got)
	}
	// 同一会话不同模型必须落到不同 key（缓存按模型隔离）
	if promptCacheKeyValue(7, 1, "") == promptCacheKeyValue(7, 2, "") {
		t.Error("不同模型应生成不同的 prompt_cache_key")
	}
}

// TestPromptCacheBreakpointsEnabled 开关打开时：
//   - prompt_cache_key 按会话+模型生成；
//   - 两处断点分别落在 system 与稳定前缀末尾（本用例即最后一条历史消息）；
//   - 断点以 content block 形态序列化。
func TestPromptCacheBreakpointsEnabled(t *testing.T) {
	enablePromptCacheSwitches(t, true, true)
	db := setupTestDB(t)
	createPromptCacheMessages(t, db, []structs.Messages{
		{ChatID: 61, Type: structs.MessagesRoleUser, Delta: "hello"},
	})

	req, err := RequestBody(61, 1, "", noTools(), db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}
	if req.PromptCacheKey != "alkaid0-chat-61-m1" {
		t.Errorf("unexpected prompt_cache_key: %q", req.PromptCacheKey)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("expected [system, user], got %d messages", len(req.Messages))
	}
	if req.Messages[0].Role != reqStruct.RoleSystem || req.Messages[0].CacheControl == nil {
		t.Errorf("system 消息应挂第一处断点: %+v", req.Messages[0].CacheControl)
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != reqStruct.RoleUser || last.CacheControl == nil {
		t.Errorf("稳定前缀末尾（最后一条历史消息）应挂第二处断点，role=%s", last.Role)
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := strings.Count(string(raw), `"cache_control"`); got != 2 {
		t.Errorf("expected exactly 2 cache_control blocks, got %d", got)
	}
	if !strings.Contains(string(raw), `"content":[{"type":"text"`) {
		t.Error("断点消息的 content 应序列化为块数组")
	}
	if !strings.Contains(string(raw), `"cache_control":{"type":"ephemeral"}`) {
		t.Error("断点应序列化为 cache_control:{\"type\":\"ephemeral\"}")
	}
}

// TestPromptCacheBreakpointStaysBeforeTailNotice 末尾注入的运行期通知块每轮都可能变化，
// 断点必须落在它**之前**的稳定历史消息上，否则通知一变整个前缀缓存就作废。
func TestPromptCacheBreakpointStaysBeforeTailNotice(t *testing.T) {
	enablePromptCacheSwitches(t, true, true)
	db := setupTestDB(t)
	createPromptCacheMessages(t, db, []structs.Messages{
		{ChatID: 62, Type: structs.MessagesRoleUser, Delta: "hello"},
	})
	chatLn := &structs.Chats{ID: 62, TemporyDataOfSession: map[string]any{
		structs.TempKeySystemNotices: "background task finished",
	}}

	req, err := RequestBody(62, 1, "", noTools(), db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	noticeIdx := -1
	for i, m := range req.Messages {
		if strings.Contains(m.Content, "Alkaid System Notice") {
			noticeIdx = i
		}
	}
	if noticeIdx != len(req.Messages)-1 {
		t.Fatalf("运行期通知应注入在消息列表末尾，got idx=%d total=%d", noticeIdx, len(req.Messages))
	}
	if req.Messages[noticeIdx].CacheControl != nil {
		t.Error("末尾注入的通知块不得承载断点（它每轮都可能变化）")
	}
	if noticeIdx == 0 {
		t.Fatal("expected a history message before the notice block")
	}
	if req.Messages[noticeIdx-1].CacheControl == nil {
		t.Error("断点应落在通知块之前的稳定历史消息上")
	}
}

// TestPromptCacheBreakpointSkipsToolRound 请求以工具轮收尾时（agentic 循环常见形态），
// 带 tool_calls 的 assistant 与 role:"tool" 结果都不承载断点（内容转块会被转换代理拒绝），
// 断点回退到本轮之前最后一条纯文本消息。
func TestPromptCacheBreakpointSkipsToolRound(t *testing.T) {
	enablePromptCacheSwitches(t, true, true)
	db := setupTestDB(t)
	createPromptCacheMessages(t, db, []structs.Messages{
		{ChatID: 63, Type: structs.MessagesRoleUser, Delta: "read a.txt"},
		{ChatID: 63, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"read","id":"call_1","parameters":{"path":"a.txt"}}]`},
		{ChatID: 63, Type: structs.MessagesRoleTool, Delta: `[{"name":"read","id":"call_1","return":"{\"ok\":true}"}]`},
	})

	req, err := RequestBody(63, 1, "", noTools(), db, "", "", cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody failed: %v", err)
	}

	last := req.Messages[len(req.Messages)-1]
	if last.Role != reqStruct.RoleTool {
		t.Fatalf("本用例应以 role:tool 结果收尾，got %s", last.Role)
	}
	for i, m := range req.Messages {
		if len(m.ToolCalls) > 0 && m.CacheControl != nil {
			t.Errorf("消息 %d 带 tool_calls，不得挂断点", i)
		}
		if m.Role == reqStruct.RoleTool && m.CacheControl != nil {
			t.Errorf("消息 %d 是 role:tool 结果，不得挂断点", i)
		}
	}
	if last.CacheControl != nil {
		t.Error("末尾的 tool 结果消息不得挂断点")
	}
	// 断点回退到本轮之前最后一条纯文本消息（第一条 user 历史消息）
	if len(req.Messages) < 2 || req.Messages[1].Role != reqStruct.RoleUser || req.Messages[1].CacheControl == nil {
		t.Errorf("断点应回退到纯文本 user 消息: %+v", req.Messages[1].CacheControl)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := strings.Count(string(raw), `"cache_control"`); got != 2 {
		t.Errorf("expected exactly 2 cache_control blocks (system + last plain text), got %d", got)
	}
}
