package actions

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// setupTitleTest 初始化标题测试环境：mock server + 配置 + 全局状态重置 + 临时目录
func setupTitleTest(t *testing.T) string {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") != "true" {
		t.Skip("ALKAID0_DEBUG_MOCKSERVER not set, skipping test")
	}

	// 启动 mock server（serverOnce.Do 保证只启动一次）
	openai.StartServerTask()

	setupConfigForTest()
	config.GlobalConfig.Agent.TitleModel = 1

	// 清理全局状态，避免与其他测试互相影响
	sessions = map[string]*sessionObj{}
	sessLock = &sync.Mutex{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	connCallLock = &sync.Mutex{}
	sessionConnMap = map[string][]uint64{}
	sessionConnLock = &sync.Mutex{}
	bindedSessionOnConn = map[uint64][]string{}

	// 创建临时工作目录
	tmpDir, err := os.MkdirTemp("", "alkaid0_title_test_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	return tmpDir
}

// newTitleTestSession 创建会话（第一客户端）并 attach 第二客户端，返回 sessionID
func newTitleTestSession(t *testing.T, tmpDir string, calls2 chan ReceivedCall) string {
	calls := make(chan ReceivedCall, 200)
	callFunc := func(name string, data any, _ *string) error {
		calls <- ReceivedCall{Name: name, Data: data}
		return nil
	}
	resp, err := SessionNew(SessionNewRequest{Cwd: tmpDir}, callFunc, 1)
	if err != nil {
		t.Fatalf("SessionNew failed: %v", err)
	}
	sessionID := resp.SessionID
	if sessionID == "" {
		t.Fatal("empty session id")
	}
	// attach 第二个客户端（用来接收广播）
	callFunc2 := func(name string, data any, _ *string) error {
		calls2 <- ReceivedCall{Name: name, Data: data}
		return nil
	}
	if _, err = SessionResume(SessionResumeRequest{Cwd: tmpDir, SessionID: sessionID}, callFunc2, 2); err != nil {
		t.Fatalf("SessionResume failed: %v", err)
	}
	return sessionID
}

// matchSessionTitle 匹配 ACP v2 标准 session_info_update 事件并返回标题
func matchSessionTitle(rc ReceivedCall) (string, bool) {
	if rc.Name != "session/update" {
		return "", false
	}
	if su, ok := rc.Data.(SessionUpdate); ok {
		if upd, ok2 := su.Update.(SessionUpdateUpdate); ok2 {
			if upd.SessionUpdate == "session_info_update" {
				return upd.Title, true
			}
		}
	}
	return "", false
}

// matchSummaryDone 匹配摘要完成事件（text 非空，排除启动事件）
func matchSummaryDone(rc ReceivedCall) bool {
	if rc.Name != "session/update" {
		return false
	}
	if su, ok := rc.Data.(SessionUpdate); ok {
		if upd, ok2 := su.Update.(SessionUpdateUpdate); ok2 {
			if upd.SessionUpdate == "alk.cxykevin.top/summary" {
				if content, ok := upd.Content.(u.H); ok {
					text, _ := content["text"].(string)
					return text != ""
				}
			}
		}
	}
	return false
}

// waitNoSessionTitle 在等待期间确认未收到 session_title 事件，超时返回 true
func waitNoSessionTitle(ch <-chan ReceivedCall, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case v := <-ch:
			if _, ok := matchSessionTitle(v); ok {
				return false
			}
		case <-deadline:
			return true
		}
	}
}

// queryChatTitle 从 DB 查询会话标题字段
func queryChatTitle(t *testing.T, tmpDir string) (title, aiTitle string) {
	db, err := loadDB(tmpDir)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	defer closeDB(tmpDir)
	var chat structs.Chats
	if err := db.First(&chat).Error; err != nil {
		t.Fatalf("query chat failed: %v", err)
	}
	return chat.Title, chat.AITitle
}

// TestPromptIntegration_AutoTitle 首次正常请求完整响应后自动生成 AI 标题
func TestPromptIntegration_AutoTitle(t *testing.T) {
	tmpDir := setupTitleTest(t)
	calls2 := make(chan ReceivedCall, 200)
	sessionID := newTitleTestSession(t, tmpDir, calls2)

	// 发送正常 prompt（非命令轮）
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "Hello mock"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt failed: %v", err)
	}

	// 等待 session_title 事件（标题生成异步完成）
	rc, ok := waitForUpdate(calls2, func(rc ReceivedCall) bool {
		_, matched := matchSessionTitle(rc)
		return matched
	}, 15*time.Second)
	if !ok {
		t.Fatal("did not receive session_title event")
	}
	title, _ := matchSessionTitle(rc)
	if title == "" {
		t.Fatal("title should not be empty")
	}

	// DB 验证：AITitle 已写入，Title（用户设置）为空
	dbTitle, dbAITitle := queryChatTitle(t, tmpDir)
	if dbAITitle != title {
		t.Errorf("DB AITitle mismatch: got %q, want %q", dbAITitle, title)
	}
	if dbTitle != "" {
		t.Errorf("DB Title should be empty, got %q", dbTitle)
	}

	closeSession(sessionID)
}

// TestPromptIntegration_NoTitleOnCommandTurn 命令轮不算正常请求，不触发标题生成
func TestPromptIntegration_NoTitleOnCommandTurn(t *testing.T) {
	tmpDir := setupTitleTest(t)
	calls2 := make(chan ReceivedCall, 200)
	sessionID := newTitleTestSession(t, tmpDir, calls2)

	// 命令轮（/version）
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "/version"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt failed: %v", err)
	}

	if !waitNoSessionTitle(calls2, 3*time.Second) {
		t.Fatal("unexpected session_title event after command turn")
	}
	closeSession(sessionID)
}

// TestPromptIntegration_TitleCommand /title 命令设置与还原用户标题
func TestPromptIntegration_TitleCommand(t *testing.T) {
	tmpDir := setupTitleTest(t)
	calls2 := make(chan ReceivedCall, 200)
	sessionID := newTitleTestSession(t, tmpDir, calls2)

	// 先跑一轮正常 prompt 生成 AI 标题
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "Hello mock"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt failed: %v", err)
	}
	rc, ok := waitForUpdate(calls2, func(rc ReceivedCall) bool {
		_, matched := matchSessionTitle(rc)
		return matched
	}, 15*time.Second)
	if !ok {
		t.Fatal("did not receive session_title event")
	}
	aiTitle, _ := matchSessionTitle(rc)

	// /title 设置用户标题
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "/title 自定义标题"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt /title failed: %v", err)
	}
	rc, ok = waitForUpdate(calls2, func(rc ReceivedCall) bool {
		_, matched := matchSessionTitle(rc)
		return matched
	}, 5*time.Second)
	if !ok {
		t.Fatal("did not receive session_title event after /title")
	}
	title, _ := matchSessionTitle(rc)
	if title != "自定义标题" {
		t.Errorf("expected title 自定义标题, got %q", title)
	}
	dbTitle, dbAITitle := queryChatTitle(t, tmpDir)
	if dbTitle != "自定义标题" {
		t.Errorf("DB Title mismatch: got %q", dbTitle)
	}
	if dbAITitle != aiTitle {
		t.Errorf("DB AITitle should be unchanged: got %q, want %q", dbAITitle, aiTitle)
	}

	// /title 还原：清除用户标题，展示回退到 AI 标题
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "/title"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt /title reset failed: %v", err)
	}
	rc, ok = waitForUpdate(calls2, func(rc ReceivedCall) bool {
		_, matched := matchSessionTitle(rc)
		return matched
	}, 5*time.Second)
	if !ok {
		t.Fatal("did not receive session_title event after /title reset")
	}
	resetTitle, _ := matchSessionTitle(rc)
	if resetTitle != aiTitle {
		t.Errorf("expected fallback to AI title %q, got %q", aiTitle, resetTitle)
	}
	dbTitle, dbAITitle = queryChatTitle(t, tmpDir)
	if dbTitle != "" {
		t.Errorf("DB Title should be cleared, got %q", dbTitle)
	}
	if dbAITitle != aiTitle {
		t.Errorf("DB AITitle should be unchanged: got %q, want %q", dbAITitle, aiTitle)
	}
	closeSession(sessionID)
}

// TestPromptIntegration_NoTitleOnCompressWithUserTitle 用户设置了手动标题时 compress 跳过重生成
func TestPromptIntegration_NoTitleOnCompressWithUserTitle(t *testing.T) {
	tmpDir := setupTitleTest(t)
	calls2 := make(chan ReceivedCall, 300)
	sessionID := newTitleTestSession(t, tmpDir, calls2)

	// 先跑一轮正常 prompt 生成 AI 标题
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "Hello mock"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt failed: %v", err)
	}
	if _, ok := waitForUpdate(calls2, func(rc ReceivedCall) bool {
		_, matched := matchSessionTitle(rc)
		return matched
	}, 15*time.Second); !ok {
		t.Fatal("did not receive session_title event")
	}

	// 设置用户标题
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "/title 用户标题"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt /title failed: %v", err)
	}
	if _, ok := waitForUpdate(calls2, func(rc ReceivedCall) bool {
		_, matched := matchSessionTitle(rc)
		return matched
	}, 5*time.Second); !ok {
		t.Fatal("did not receive session_title event after /title")
	}

	// /compress：用户已设置手动标题 → 跳过重生成
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "/compress"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt /compress failed: %v", err)
	}
	if _, ok := waitForUpdate(calls2, matchSummaryDone, 15*time.Second); !ok {
		t.Fatal("did not receive summary done event")
	}
	if !waitNoSessionTitle(calls2, 3*time.Second) {
		t.Fatal("unexpected session_title event after compress with user title")
	}
	closeSession(sessionID)
}

// TestPromptIntegration_CompressRegen 无用户标题时 compress 后重生成 AI 标题
func TestPromptIntegration_CompressRegen(t *testing.T) {
	tmpDir := setupTitleTest(t)
	calls2 := make(chan ReceivedCall, 300)
	sessionID := newTitleTestSession(t, tmpDir, calls2)

	// 先跑一轮正常 prompt 生成 AI 标题
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "Hello mock"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt failed: %v", err)
	}
	if _, ok := waitForUpdate(calls2, func(rc ReceivedCall) bool {
		_, matched := matchSessionTitle(rc)
		return matched
	}, 15*time.Second); !ok {
		t.Fatal("did not receive session_title event")
	}

	// /compress：无用户标题 → 触发标题重生成
	if _, err := SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "/compress"}}}, nil, 1); err != nil {
		t.Fatalf("SessionPrompt /compress failed: %v", err)
	}
	if _, ok := waitForUpdate(calls2, matchSummaryDone, 15*time.Second); !ok {
		t.Fatal("did not receive summary done event")
	}
	rc, ok := waitForUpdate(calls2, func(rc ReceivedCall) bool {
		_, matched := matchSessionTitle(rc)
		return matched
	}, 15*time.Second)
	if !ok {
		t.Fatal("did not receive session_title event after compress")
	}
	title2, _ := matchSessionTitle(rc)
	if title2 == "" {
		t.Fatal("title should not be empty")
	}
	_, dbAITitle := queryChatTitle(t, tmpDir)
	if dbAITitle != title2 {
		t.Errorf("DB AITitle mismatch after compress: got %q, want %q", dbAITitle, title2)
	}
	closeSession(sessionID)
}

// TestSessionList_Fallback 会话列表标题回退链：用户标题 → AI 标题 → Untitled(N)
func TestSessionList_Fallback(t *testing.T) {
	tmpDir := setupTitleTest(t)
	// 手动创建 .alkaid0 目录并插入带标题的会话
	if err := os.MkdirAll(filepath.Join(tmpDir, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("failed to create .alkaid0: %v", err)
	}
	db, err := loadDB(tmpDir)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	chats := []structs.Chats{
		{Title: "用户标题", AITitle: "AI标题"},
		{AITitle: "AI标题2"},
		{},
	}
	for i := range chats {
		if err := db.Create(&chats[i]).Error; err != nil {
			t.Fatalf("failed to create chat %d: %v", i, err)
		}
	}
	// 设置递增的最后活动时间，保证排序确定：id 越大越新 → 列表按 id 倒序。
	// 直接改库绕开 GORM autoUpdateTime 回调。
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for i := range chats {
		if err := db.Exec("UPDATE chats SET updated_at = ? WHERE id = ?",
			base.Add(time.Duration(i)*time.Hour), chats[i].ID).Error; err != nil {
			t.Fatalf("failed to set updated_at for chat %d: %v", i, err)
		}
	}
	closeDB(tmpDir)

	// 调用 SessionList
	resp, err := SessionList(SessionListRequest{Cwd: tmpDir}, nil, 1)
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	if len(resp.Sessions) != len(chats) {
		t.Fatalf("expected %d sessions, got %d", len(chats), len(resp.Sessions))
	}
	// 新→旧：chats[2] → chats[1] → chats[0]
	expects := []string{
		fmt.Sprintf("Untitled(%d)", chats[2].ID),
		"AI标题2",
		"用户标题",
	}
	for i, e := range expects {
		if resp.Sessions[i].Title != e {
			t.Errorf("Session[%d] title mismatch: got %q, want %q", i, resp.Sessions[i].Title, e)
		}
	}
}
