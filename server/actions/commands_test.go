package actions

import (
	"strings"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/loop"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestInitCommandRegistered 验证 /init 命令已注册且字段完整。
func TestInitCommandRegistered(t *testing.T) {
	cmd, ok := commandMaps["/init"]
	if !ok {
		t.Fatal("/init command not registered")
	}
	if cmd.Description == "" {
		t.Error("/init command has empty Description")
	}
	if cmd.Hint == "" {
		t.Error("/init command has empty Hint")
	}
	if cmd.Function == nil {
		t.Error("/init command has nil Function")
	}
}

// TestInitPromptRendered 验证 /init 提示词模板可渲染，
// 且包含 AGENTS.md 与定制的 Alkaid0 引导语。
func TestInitPromptRendered(t *testing.T) {
	rendered, err := prompts.Render(prompts.InitTemplate, struct{}{})
	if err != nil {
		t.Fatalf("Render InitTemplate error = %v", err)
	}
	for _, want := range []string{
		"AGENTS.md",
		"This file provides guidance to Alkaid0 agent when working with code in this repository.",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered init prompt missing %q", want)
		}
	}
}

// TestInitCommandPersistsCommandString /init 的历史记录是命令字符串本身：
// 持久化一条 delta="/init" 的用户消息并以真实 msg_ messageId 广播（回放/直播一致）；
// 展开的初始化指令不落库，历史回放时由 build 层固定注入 init 提示词（见 replayUserContent）。
func TestInitCommandPersistsCommandString(t *testing.T) {
	// 本用例只关心消息构造；固定配置，避免受其他用例的全局配置影响。
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{})
	t.Cleanup(restoreCfg)
	config.GlobalConfig.Agent.DisablePromptPreprocess = true

	dir, db, ids := newSessionListDB(t, 1)
	chatID := ids[0]
	sessionID := cwd2SessionID(dir, chatID)
	sess := &structs.Chats{ID: chatID, DB: db}
	obj := &sessionObj{cwd: dir, id: chatID, session: sess, loop: loop.New(sess)}

	var mu sync.Mutex
	var updates []SessionUpdate
	registerCaptureConn(t, 7711, sessionID, func(su SessionUpdate) {
		mu.Lock()
		updates = append(updates, su)
		mu.Unlock()
	})

	cmd, ok := commandMaps["/init"]
	if !ok {
		t.Fatal("/init command not registered")
	}
	if !cmd.NoCmdMessage {
		t.Error("/init 应设置 NoCmdMessage（Function 自行广播 user_message）")
	}

	wait, err := cmd.Function(obj, "")
	if err != nil {
		t.Fatalf("/init Function error = %v", err)
	}
	if !wait {
		t.Error("/init 应返回 wait=true（异步命令，收尾 idle 由 loop 回调负责）")
	}

	// 历史记录：只有命令字符串本身这一条用户消息，没有展开后的模板消息。
	var msgs []structs.Messages
	if err := db.Where("chat_id = ?", chatID).Order("id").Find(&msgs).Error; err != nil {
		t.Fatalf("query messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want exactly 1 message, got %d", len(msgs))
	}
	msg := msgs[0]
	if msg.Type != structs.MessagesRoleUser {
		t.Errorf("message type = %d, want user", msg.Type)
	}
	if msg.Delta != "/init" {
		t.Errorf("message delta = %q, want %q", msg.Delta, "/init")
	}

	// 命令消息不携带引用：初始化指令不落库，历史回放时由 build 层固定注入
	// （见 provider/request/build replayUserContent）。
	if len(msg.Refers) != 0 {
		t.Errorf("/init 消息不应携带引用, got %d", len(msg.Refers))
	}

	// 广播：真实 msg_ messageId + 内容为命令字符串。
	mu.Lock()
	got := append([]SessionUpdate(nil), updates...)
	mu.Unlock()
	sawUserMessage := false
	wantMsgID := msgID(msg.ID)
	for _, su := range got {
		if su.SessionID != sessionID {
			continue
		}
		inner, ok := su.Update.(SessionUpdateUpdate)
		if !ok || inner.SessionUpdate != "user_message" {
			continue
		}
		sawUserMessage = true
		if inner.MessageID != wantMsgID {
			t.Errorf("user_message messageId = %q, want %q", inner.MessageID, wantMsgID)
		}
		content, ok := inner.Content.([]u.H)
		if !ok || len(content) != 1 {
			t.Fatalf("unexpected user_message content %#v", inner.Content)
		}
		if text, _ := content[0]["text"].(string); text != "/init" {
			t.Errorf("user_message text = %q, want %q", text, "/init")
		}
	}
	if !sawUserMessage {
		t.Error("no user_message broadcast for /init")
	}
}

// TestPhraseCommandStoresVerbatim /s 的入库与回显都是短语原始内容：
// 短语含代码块（普通消息路径会被提示词预处理改写）时，/s 仍按原文落库并广播。
func TestPhraseCommandStoresVerbatim(t *testing.T) {
	// 固定配置；不设置 DisablePromptPreprocess——预处理保持开启，验证 /s 的跳过逻辑。
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{})
	t.Cleanup(restoreCfg)

	phraseText := "请解释这段代码：\n```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```"
	config.GlobalConfig.Context.Phrase.Enable = true
	config.GlobalConfig.Context.Phrase.Phrases = []cfgStructs.Phrase{
		{Short: "kc", Text: phraseText},
	}

	dir, db, ids := newSessionListDB(t, 2)
	controlChatID, chatID := ids[0], ids[1]
	sessionID := cwd2SessionID(dir, chatID)
	sess := &structs.Chats{ID: chatID, DB: db}
	obj := &sessionObj{cwd: dir, id: chatID, session: sess, loop: loop.New(sess)}

	// 对照组：同一环境下普通消息路径被分类器改写，证明此环境预处理真实生效。
	controlID, err := funcs.UserAddMsgWithID(&structs.Chats{ID: controlChatID, DB: db}, phraseText, nil)
	if err != nil {
		t.Fatalf("control UserAddMsgWithID failed: %v", err)
	}
	var controlMsg structs.Messages
	if err := db.First(&controlMsg, controlID).Error; err != nil {
		t.Fatalf("query control message: %v", err)
	}
	if controlMsg.Delta == phraseText || !strings.Contains(controlMsg.Delta, "[path:@temp/prompt/code-") {
		t.Fatalf("对照组：预处理未生效（用例将失去意义），delta=%q", controlMsg.Delta)
	}

	var mu sync.Mutex
	var updates []SessionUpdate
	registerCaptureConn(t, 7712, sessionID, func(su SessionUpdate) {
		mu.Lock()
		updates = append(updates, su)
		mu.Unlock()
	})

	cmd, ok := commandMaps["/s"]
	if !ok {
		t.Fatal("/s command not registered")
	}
	if !cmd.NoCmdMessage {
		t.Error("/s 应设置 NoCmdMessage（Function 自行广播 user_message）")
	}

	wait, err := cmd.Function(obj, "kc")
	if err != nil {
		t.Fatalf("/s Function error = %v", err)
	}
	if !wait {
		t.Error("/s 应返回 wait=true（异步命令，收尾 idle 由 loop 回调负责）")
	}

	// 入库：短语原文（不经分类器改写）
	var msgs []structs.Messages
	if err := db.Where("chat_id = ?", chatID).Order("id").Find(&msgs).Error; err != nil {
		t.Fatalf("query messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want exactly 1 message, got %d", len(msgs))
	}
	msg := msgs[0]
	if msg.Type != structs.MessagesRoleUser {
		t.Errorf("message type = %d, want user", msg.Type)
	}
	if msg.Delta != phraseText {
		t.Errorf("入库内容应为短语原文:\ngot  %q\nwant %q", msg.Delta, phraseText)
	}
	var segCount int64
	db.Model(&structs.ClassifySegment{}).Where("message_id = ?", msg.ID).Count(&segCount)
	if segCount != 0 {
		t.Errorf("/s 消息不应产生分类段, got %d", segCount)
	}

	// 回显：真实 msg_ messageId + 短语原文
	mu.Lock()
	got := append([]SessionUpdate(nil), updates...)
	mu.Unlock()
	sawUserMessage := false
	wantMsgID := msgID(msg.ID)
	for _, su := range got {
		if su.SessionID != sessionID {
			continue
		}
		inner, ok := su.Update.(SessionUpdateUpdate)
		if !ok || inner.SessionUpdate != "user_message" {
			continue
		}
		sawUserMessage = true
		if inner.MessageID != wantMsgID {
			t.Errorf("user_message messageId = %q, want %q", inner.MessageID, wantMsgID)
		}
		content, ok := inner.Content.([]u.H)
		if !ok || len(content) != 1 {
			t.Fatalf("unexpected user_message content %#v", inner.Content)
		}
		if text, _ := content[0]["text"].(string); text != phraseText {
			t.Errorf("user_message text 应为短语原文:\ngot  %q\nwant %q", text, phraseText)
		}
	}
	if !sawUserMessage {
		t.Error("no user_message broadcast for /s")
	}
}

// TestFeedbackCommandRegistered 验证 /feedback 命令已注册且字段完整。
func TestFeedbackCommandRegistered(t *testing.T) {
	cmd, ok := commandMaps["/feedback"]
	if !ok {
		t.Fatal("/feedback command not registered")
	}
	if cmd.Description == "" {
		t.Error("/feedback command has empty Description")
	}
	if cmd.Hint == "" {
		t.Error("/feedback command has empty Hint")
	}
	if cmd.Function == nil {
		t.Error("/feedback command has nil Function")
	}
}
