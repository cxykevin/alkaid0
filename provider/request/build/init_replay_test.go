package build

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
)

// TestReplayUserContent 普通用户消息原样透传；/init 命令消息（delta 为命令字符串本身）
// 固定注入 init 提示词并丢弃引用（历史里过渡格式的模板引用不得重复注入）。
func TestReplayUserContent(t *testing.T) {
	initPrompt, err := prompts.Render(prompts.InitTemplate, struct{}{})
	if err != nil {
		t.Fatalf("Render InitTemplate: %v", err)
	}

	refers := structs.MessagesReferList{{FileType: structs.MessagesReferTypeText, Origin: []byte("ref")}}
	prompt, gotRefers, err := replayUserContent("hello", refers)
	if err != nil {
		t.Fatalf("replayUserContent: %v", err)
	}
	if prompt != "hello" {
		t.Errorf("普通消息 prompt = %q, want %q", prompt, "hello")
	}
	if len(gotRefers) != 1 || string(gotRefers[0].Origin) != "ref" {
		t.Errorf("普通消息引用应原样保留, got %#v", gotRefers)
	}

	legacyRefers := structs.MessagesReferList{{FileType: structs.MessagesReferTypeText, Origin: []byte(initPrompt)}}
	prompt, gotRefers, err = replayUserContent("/init", legacyRefers)
	if err != nil {
		t.Fatalf("replayUserContent(/init): %v", err)
	}
	if prompt != initPrompt {
		t.Error("/init 命令消息应固定注入 init 提示词，而不是命令字符串本身")
	}
	if gotRefers != nil {
		t.Errorf("/init 命令消息的引用应被丢弃, got %#v", gotRefers)
	}
}

// TestRequestBody_InitCommandMessageReplaysInitPrompt /init 的历史记录是命令字符串本身（delta="/init"），
// 但历史回放固定注入 init 提示词：用户消息内容（user_wrap）必须是初始化指令，而不是 "/init" 命令字符串本身；
// 引用（含过渡格式的模板引用）不得进入回放，同一模板不得重复出现。
func TestRequestBody_InitCommandMessageReplaysInitPrompt(t *testing.T) {
	db := setupTestDB(t)
	setupTestConfig()

	initPrompt, err := prompts.Render(prompts.InitTemplate, struct{}{})
	if err != nil {
		t.Fatalf("Render InitTemplate: %v", err)
	}
	if !strings.Contains(initPrompt, "AGENTS.md") {
		t.Fatalf("InitTemplate 渲染结果异常: %q", initPrompt)
	}

	chatID := uint32(49)
	msgs := []structs.Messages{
		{ChatID: chatID, Type: structs.MessagesRoleUser, Delta: "/init"},
		{ChatID: chatID, Type: structs.MessagesRoleAgent, Delta: "done"},
		// 过渡格式：模板曾作为 text refer 附在 /init 消息上（历史遗留行）
		{ChatID: chatID, Type: structs.MessagesRoleUser, Delta: "/init", Refers: structs.MessagesReferList{{
			FileType: structs.MessagesReferTypeText,
			Origin:   []byte(initPrompt),
		}}},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	chatLn := eventTestChatLn(map[string]*structs.TraceEvent{}, map[string]trace.FileBlock{})
	chatLn.ID = chatID
	chatLn.DB = db
	chatLn.NowAgent = ""
	contents := requestFor(t, db, chatID, chatLn)

	want, err := prompts.Render(prompts.UserWrapTemplate, struct {
		Prompt string
		Refers structs.MessagesReferList
	}{Prompt: initPrompt})
	if err != nil {
		t.Fatalf("Render UserWrapTemplate: %v", err)
	}
	matched := 0
	for _, c := range contents {
		if c == want {
			matched++
		}
		if strings.Contains(c, "<refers>") {
			t.Errorf("引用不得进入回放: %q", c)
		}
		if strings.Contains(c, "<user_prompt>\n/init\n</user_prompt>") {
			t.Errorf("命令字符串本身不得作为回放内容: %q", c)
		}
	}
	if matched != 2 {
		t.Errorf("两条 /init 消息都必须注入 init 提示词, got %d", matched)
	}
}
