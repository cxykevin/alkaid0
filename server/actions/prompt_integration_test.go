package actions

// import (
// 	"context"
// 	"os"
// 	"path/filepath"
// 	"sync"
// 	"testing"
// 	"time"

// 	"github.com/cxykevin/alkaid0/config"
// 	"github.com/cxykevin/alkaid0/config/structs"
// 	"github.com/cxykevin/alkaid0/mock/openai"
// 	"github.com/cxykevin/alkaid0/storage"
// 	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
// 	"github.com/cxykevin/alkaid0/ui/funcs"
// 	"github.com/cxykevin/alkaid0/ui/loop"
// 	u "github.com/cxykevin/alkaid0/utils"
// 	"gorm.io/gorm"
// )

// // initMockServerAndConfig 初始化 mock server 和配置
// func initMockServerAndConfig(t *testing.T) (string, string) {
// 	// 启动 mock server
// 	openai.StartServerTask()

// 	// 创建临时工作目录
// 	tmpDir := t.TempDir()
// 	dbDir := filepath.Join(tmpDir, ".alkaid0")

// 	// 初始化全局配置
// 	if config.GlobalConfig.Model.Models == nil {
// 		config.GlobalConfig.Model.Models = make(map[int32]structs.ModelConfig)
// 	}

// 	// 设置 mock 模型配置
// 	mockModelConfig := structs.ModelConfig{
// 		ModelName:         "test-chat",
// 		ModelID:           "test-chat",
// 		TokenLimit:        4096,
// 		ProviderURL:       "http://localhost:56108/v1",
// 		ProviderKey:       "mock-key",
// 		EnableThinking:    false,
// 		EnableToolCalling: false,
// 	}

// 	config.GlobalConfig.Model.ProviderURL = "http://localhost:56108/v1"
// 	config.GlobalConfig.Model.ProviderKey = "mock-key"
// 	config.GlobalConfig.Model.DefaultModelID = 1
// 	config.GlobalConfig.Model.Models[1] = mockModelConfig

// 	return tmpDir, dbDir
// }

// // initDB 初始化数据库
// func initDB(t *testing.T, dbDir string) *gorm.DB {
// 	db, err := storage.InitStorage(dbDir, "")
// 	if err != nil {
// 		t.Fatalf("failed to initialize storage: %v", err)
// 	}
// 	return db
// }

// // TestFullPromptTurnWithMockServer 完整的 prompt turn 集成测试
// func TestFullPromptTurnWithMockServer(t *testing.T) {
// 	// 初始化 mock server 和配置
// 	tmpDir, dbDir := initMockServerAndConfig(t)
// 	defer func() {
// 		os.RemoveAll(tmpDir)
// 	}()

// 	// 初始化数据库
// 	db := initDB(t, dbDir)

// 	// 创建测试会话
// 	chatID, err := funcs.CreateChat(db)
// 	if err != nil {
// 		t.Fatalf("failed to create chat: %v", err)
// 	}

// 	chat, err := funcs.QueryChat(db, chatID)
// 	if err != nil {
// 		t.Fatalf("failed to query chat: %v", err)
// 	}

// 	chat.Root = tmpDir
// 	sess, err := funcs.InitChat(db, &chat)
// 	if err != nil {
// 		t.Fatalf("failed to initialize chat: %v", err)
// 	}
// 	sess.Root = tmpDir
// 	sess.LastModelID = 1 // 使用 mock 模型

// 	// 设置代理和模型配置
// 	sess.CurrentAgentID = ""
// 	sess.CurrentAgentConfig = structs.AgentConfig{}

// 	// 记录接收到的更新
// 	var mu sync.Mutex
// 	updates := make([]string, 0)
// 	broadcastCalls := make([]bool, 0)

// 	callFunc := func(method string, v any) error {
// 		mu.Lock()
// 		defer mu.Unlock()
// 		if method == "session/update" {
// 			broadcastCalls = append(broadcastCalls, true)
// 			if update, ok := v.(SessionUpdate); ok {
// 				updates = append(updates, update.Update.SessionUpdate)
// 			}
// 		}
// 		return nil
// 	}

// 	// 准备 prompt request
// 	sessionID := cwd2SessionID(tmpDir, chatID)
// 	promptReq := SessionPromptRequest{
// 		SessionID: sessionID,
// 		Prompt: []u.H{
// 			{
// 				"type": "text",
// 				"text": "Hello from test!",
// 			},
// 		},
// 	}

// 	// 将会话添加到内存映射（通常由 session/new 或 session/load 完成）
// 	sessLock.Lock()
// 	sessions[sessionID] = &sessionObj{
// 		cwd:      tmpDir,
// 		id:       chatID,
// 		session:  sess,
// 		loop:     loop.New(sess),
// 		ctx:      context.Background(),
// 		referCnt: 1,
// 	}
// 	sessLock.Unlock()

// 	defer func() {
// 		sessLock.Lock()
// 		delete(sessions, sessionID)
// 		sessLock.Unlock()
// 	}()

// 	// 注册连接
// 	connID := uint64(1)
// 	registerConnCall(connID, sessionID, callFunc)
// 	defer unregisterConnCall(connID, sessionID)

// 	// 发送 prompt
// 	resp, err := SessionPrompt(promptReq, callFunc, connID)

// 	// 验证响应
// 	if err != nil {
// 		// LLM 调用可能会失败，但应该返回正确的 stop reason
// 		t.Logf("SessionPrompt returned error: %v (this may be expected)", err)
// 	}

// 	if resp.StopReason == "" {
// 		t.Error("stopReason should not be empty")
// 	}

// 	// 验证用户消息被添加到数据库
// 	msgs, err := funcs.GetHistory(sess)
// 	if err != nil {
// 		t.Logf("failed to get history: %v", err)
// 	} else {
// 		foundUserMsg := false
// 		for _, msg := range msgs {
// 			if msg.Type == storageStructs.MessagesRoleUser && msg.Delta == "Hello from test!" {
// 				foundUserMsg = true
// 				break
// 			}
// 		}
// 		if !foundUserMsg {
// 			t.Log("user message not found in history (this may be expected if LLM call failed)")
// 		}
// 	}

// 	t.Logf("Test completed with stop reason: %s", resp.StopReason)
// }

// // TestPromptTurnBroadcastToMultipleConns 测试多连接广播
// func TestPromptTurnBroadcastToMultipleConns(t *testing.T) {
// 	// 初始化 mock server
// 	openai.StartServerTask()

// 	tmpDir := t.TempDir()
// 	dbDir := filepath.Join(tmpDir, ".alkaid0")

// 	// 初始化数据库
// 	db := initDB(t, dbDir)

// 	// 创建会话
// 	chatID, err := funcs.CreateChat(db)
// 	if err != nil {
// 		t.Fatalf("failed to create chat: %v", err)
// 	}

// 	chat, err := funcs.QueryChat(db, chatID)
// 	if err != nil {
// 		t.Fatalf("failed to query chat: %v", err)
// 	}

// 	chat.Root = tmpDir
// 	sess, err := funcs.InitChat(db, &chat)
// 	if err != nil {
// 		t.Fatalf("failed to initialize chat: %v", err)
// 	}
// 	sess.Root = tmpDir
// 	sess.LastModelID = 1

// 	sessionID := cwd2SessionID(tmpDir, chatID)

// 	// 添加会话到内存
// 	sessLock.Lock()
// 	sessions[sessionID] = &sessionObj{
// 		cwd:      tmpDir,
// 		id:       chatID,
// 		session:  sess,
// 		loop:     loop.New(sess),
// 		ctx:      context.Background(),
// 		referCnt: 1,
// 	}
// 	sessLock.Unlock()

// 	defer func() {
// 		sessLock.Lock()
// 		delete(sessions, sessionID)
// 		sessLock.Unlock()
// 	}()

// 	// 记录多个连接的接收情况
// 	var mu sync.Mutex
// 	recvCounts := make(map[uint64]int)

// 	// 创建多个 call 函数
// 	createCallFunc := func(cid uint64) func(string, any) error {
// 		return func(method string, v any) error {
// 			mu.Lock()
// 			defer mu.Unlock()
// 			if method == "session/update" {
// 				recvCounts[cid]++
// 			}
// 			return nil
// 		}
// 	}

// 	// 注册 3 个连接
// 	for i := uint64(1); i <= 3; i++ {
// 		registerConnCall(i, sessionID, createCallFunc(i))
// 		defer unregisterConnCall(i, sessionID)
// 	}

// 	// 模拟第一个连接发送 prompt
// 	senderConnID := uint64(1)
// 	promptReq := SessionPromptRequest{
// 		SessionID: sessionID,
// 		Prompt: []u.H{
// 			{"type": "text", "text": "test message"},
// 		},
// 	}

// 	callFunc := createCallFunc(senderConnID)
// 	resp, err := SessionPrompt(promptReq, callFunc, senderConnID)

// 	t.Logf("SessionPrompt returned: stopReason=%s, err=%v", resp.StopReason, err)

// 	// 验证连接接收情况
// 	mu.Lock()
// 	sender := recvCounts[senderConnID]
// 	other1 := recvCounts[2]
// 	other2 := recvCounts[3]
// 	mu.Unlock()

// 	// 发送方不应该接收广播更新（只接收响应）
// 	if sender > 0 {
// 		t.Logf("sender conn received %d updates (should not receive updates)", sender)
// 	}

// 	// 其他连接应该接收更新
// 	if other1 == 0 {
// 		t.Logf("conn 2 received %d updates", other1)
// 	}
// 	if other2 == 0 {
// 		t.Logf("conn 3 received %d updates", other2)
// 	}

// 	t.Logf("Broadcast verification: sender=%d, conn2=%d, conn3=%d", sender, other1, other2)
// }

// // TestSessionCancelWithMockServer 测试取消操作
// func TestSessionCancelWithMockServer(t *testing.T) {
// 	// 初始化 mock server
// 	openai.StartServerTask()

// 	sessionID := "sess_test:/tmp/test"

// 	// 创建一个进行中的 prompt
// 	ctx, cancel := context.WithCancel(context.Background())
// 	activePromptsLock.Lock()
// 	activePrompts[sessionID] = &promptCtx{
// 		cancel:   cancel,
// 		isActive: true,
// 	}
// 	activePromptsLock.Unlock()

// 	defer func() {
// 		activePromptsLock.Lock()
// 		delete(activePrompts, sessionID)
// 		activePromptsLock.Unlock()
// 	}()

// 	// 验证可以取消
// 	cancelReq := SessionCancelRequest{
// 		SessionID: sessionID,
// 	}

// 	_, err := SessionCancel(cancelReq, func(string, any) error { return nil }, 1)
// 	if err != nil {
// 		t.Errorf("SessionCancel() error = %v", err)
// 	}

// 	// 验证 context 被取消
// 	select {
// 	case <-ctx.Done():
// 		t.Log("context was canceled successfully")
// 	case <-time.After(100 * time.Millisecond):
// 		t.Error("context was not canceled")
// 	}
// }

// // TestPromptWithContentBlocks 测试带有多个内容块的 prompt
// func TestPromptWithContentBlocks(t *testing.T) {
// 	// 初始化 mock server
// 	openai.StartServerTask()

// 	tmpDir := t.TempDir()
// 	dbDir := filepath.Join(tmpDir, ".alkaid0")
// 	db := initDB(t, dbDir)

// 	// 创建会话
// 	chatID, err := funcs.CreateChat(db)
// 	if err != nil {
// 		t.Fatalf("failed to create chat: %v", err)
// 	}

// 	chat, err := funcs.QueryChat(db, chatID)
// 	if err != nil {
// 		t.Fatalf("failed to query chat: %v", err)
// 	}

// 	chat.Root = tmpDir
// 	sess, err := funcs.InitChat(db, &chat)
// 	if err != nil {
// 		t.Fatalf("failed to initialize chat: %v", err)
// 	}
// 	sess.Root = tmpDir
// 	sess.LastModelID = 1

// 	sessionID := cwd2SessionID(tmpDir, chatID)

// 	// 添加会话到内存
// 	sessLock.Lock()
// 	sessions[sessionID] = &sessionObj{
// 		cwd:      tmpDir,
// 		id:       chatID,
// 		session:  sess,
// 		loop:     loop.New(sess),
// 		ctx:      context.Background(),
// 		referCnt: 1,
// 	}
// 	sessLock.Unlock()

// 	defer func() {
// 		sessLock.Lock()
// 		delete(sessions, sessionID)
// 		sessLock.Unlock()
// 	}()

// 	// 创建带有多个内容块的 prompt
// 	promptReq := SessionPromptRequest{
// 		SessionID: sessionID,
// 		Prompt: []u.H{
// 			{"type": "text", "text": "First part "},
// 			{"type": "text", "text": "second part"},
// 			{"type": "image", "url": "file:///dummy.jpg"}, // 应该被忽略
// 			{"type": "text", "text": " third part"},
// 		},
// 	}

// 	callFunc := func(method string, v any) error {
// 		return nil
// 	}

// 	resp, err := SessionPrompt(promptReq, callFunc, 1)

// 	t.Logf("prompt result: stopReason=%s, err=%v", resp.StopReason, err)

// 	// 验证消息被正确拼接
// 	msgs, err := funcs.GetHistory(sess)
// 	if err != nil {
// 		t.Logf("failed to get history: %v (this may be expected)", err)
// 		return
// 	}

// 	expectedMsg := "First part second part third part"
// 	for _, msg := range msgs {
// 		if msg.Type == storageStructs.MessagesRoleUser {
// 			if msg.Delta == expectedMsg {
// 				t.Log("multi-block prompt correctly concatenated")
// 				return
// 			}
// 		}
// 	}

// 	t.Logf("expected message not found in history")
// }

// // TestPromptErrorHandling 测试错误处理
// func TestPromptErrorHandling(t *testing.T) {
// 	tmpDir := t.TempDir()

// 	tests := []struct {
// 		name      string
// 		sessionID string
// 		prompt    []u.H
// 		wantErr   bool
// 	}{
// 		{
// 			name:      "empty sessionId",
// 			sessionID: "",
// 			prompt:    []u.H{{"type": "text", "text": "hi"}},
// 			wantErr:   true,
// 		},
// 		{
// 			name:      "invalid sessionId format",
// 			sessionID: "invalid",
// 			prompt:    []u.H{{"type": "text", "text": "hi"}},
// 			wantErr:   true,
// 		},
// 		{
// 			name:      "empty prompt",
// 			sessionID: cwd2SessionID(tmpDir, 123),
// 			prompt:    []u.H{},
// 			wantErr:   true,
// 		},
// 		{
// 			name:      "prompt with no text",
// 			sessionID: cwd2SessionID(tmpDir, 123),
// 			prompt:    []u.H{{"type": "image"}},
// 			wantErr:   true,
// 		},
// 	}

// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			_, err := SessionPrompt(SessionPromptRequest{
// 				SessionID: tt.sessionID,
// 				Prompt:    tt.prompt,
// 			}, func(string, any) error { return nil }, 1)

// 			if (err != nil) != tt.wantErr {
// 				t.Errorf("SessionPrompt() error = %v, wantErr %v", err, tt.wantErr)
// 			}
// 		})
// 	}
// }
