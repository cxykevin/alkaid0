package build

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
)

func strPtr2(s string) *string {
	return &s
}

// TestSummary_InputIncludesRecent 验证 summary 请求把最近的对话消息也作为输入喂给模型，
// 避免模型在缺少最新进展时对总结内容产生幻觉（瞎编）。
func TestSummary_InputIncludesRecent(t *testing.T) {
	setupTestConfig()
	config.GlobalConfig.Agent.SummaryModel = 1
	db := setupTestDB(t)

	// 超过 summaryKeepNumber（6）条消息，模拟真实对话：最近 6 条包含关键最新进展
	messages := []structs.Messages{
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "帮我开发一个 WebSocket 服务器，支持心跳检测"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "好的，我来设计架构。首先创建 server.go"},
		{ChatID: 1, Type: structs.MessagesRoleTool, Delta: "file created successfully"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "server.go 已创建，接下来处理心跳逻辑"},
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "心跳间隔设为 30 秒"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "已设置心跳间隔 30 秒"},
		{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "最后帮我加上断线重连"},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "断线重连逻辑已完成"},
		// 子代理 communicate 消息（AgentID 非空，主会话 compress 时应被过滤，不产生空 user 消息）
		{
			ChatID:  1,
			Type:    structs.MessagesRoleCommunicate,
			Delta:   "子代理报告：已完成代码审查，发现 3 个问题",
			AgentID: strPtr2("subagent-1"),
		},
		{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "根据审查结果修复了 3 个问题"},
	}

	for i := range messages {
		if err := db.Create(&messages[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	_, request, err := Summary(1, "", db)
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}
	if request == nil {
		t.Fatal("request is nil")
	}

	// 最近的关键进展必须出现在总结输入中
	joined := strings.Join(collectContents(request.Messages), "\n")
	for _, want := range []string{
		"心跳间隔设为 30 秒",
		"断线重连逻辑已完成",
		"根据审查结果修复了 3 个问题",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("summary input missing recent content %q", want)
		}
	}

	// 不应有空内容的消息（子代理 communicate 消息过滤）
	for i, m := range request.Messages {
		if m.Content == "" && m.ReasoningContent == nil {
			t.Errorf("message[%d] role=%s has empty content", i, m.Role)
		}
	}

	// 总结指令必须位于最后一条
	last := request.Messages[len(request.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "conversation summary generator") {
		t.Errorf("summary instruction should be the last message, got role=%s", last.Role)
	}
}

func collectContents(msgs []reqStruct.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Content)
	}
	return out
}
