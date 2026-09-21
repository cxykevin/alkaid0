package request

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
)

// ensureTestConfig 确保全局配置可用，并注册一个模型（id -> 配置）。
func ensureTestConfig(t *testing.T, id int32, cfg cfgStruct.ModelConfig) {
	t.Helper()
	if config.GlobalConfig == nil {
		config.GlobalConfig = &cfgStruct.Config{}
	}
	if config.GlobalConfig.Model.Models == nil {
		config.GlobalConfig.Model.Models = make(map[int32]cfgStruct.ModelConfig)
	}
	config.GlobalConfig.Agent.DisablePromptPreprocess = true
	config.GlobalConfig.Model.Models[id] = cfg
}

// newStateTestSession 建库、建 chat 行并返回会话对象。
func newStateTestSession(t *testing.T, modelID int32) (*storageStructs.Chats, func()) {
	t.Helper()
	db := setupTestDB(t)
	chat := storageStructs.Chats{ID: 9001, LastModelID: uint32(modelID)}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	session := &storageStructs.Chats{ID: chat.ID, DB: db, LastModelID: uint32(modelID)}
	return session, func() { u.Unwrap(db.DB()).Close() }
}

// === P1-18 / F3：SendRequest 错误返回后状态必须回到 Idle ===

// TestP118SendRequestModelNotFoundResetsState 模型不存在时立即返回，
// 状态必须从 Waiting 复位到 Idle。修复前停在 StateWaiting。
func TestP118SendRequestModelNotFoundResetsState(t *testing.T) {
	ensureTestConfig(t, 1, cfgStruct.ModelConfig{ModelName: "m", ModelID: "m", ProviderURL: "http://127.0.0.1:1", ProviderKey: "k"})
	session, cleanup := newStateTestSession(t, 777)
	defer cleanup()

	session.State = state.StateIdle
	_, err := SendRequest(context.Background(), session,
		func(string, string, uint64, structs.Usage, *string) error { return nil })
	if err == nil {
		t.Fatal("expected model-not-found error")
	}
	if session.State != state.StateIdle {
		t.Fatalf("error return left State=%v, want Idle", session.State)
	}
	if session.ToolState != 0 {
		t.Fatalf("error return left ToolState=%d, want 0", session.ToolState)
	}
}

// TestP118SendRequestBuildFailureResetsState 占位消息创建后构建请求体失败
// （chat 行缺失）时，状态必须从 GeneratingPrompt 复位到 Idle。
func TestP118SendRequestBuildFailureResetsState(t *testing.T) {
	ensureTestConfig(t, 1, cfgStruct.ModelConfig{ModelName: "m", ModelID: "m", ProviderURL: "http://127.0.0.1:1", ProviderKey: "k"})
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	// 只迁移 Messages：build.Build 重读 chats 行会失败。占位消息 Create 在未启用
	// 外键时成功（走 GeneratingPrompt 路径）、启用外键时失败（走 Waiting 路径），
	// 两条路径都必须把状态复位到 Idle。
	if err := db.AutoMigrate(&storageStructs.Messages{}); err != nil {
		t.Fatalf("migrate messages: %v", err)
	}
	session := &storageStructs.Chats{ID: 9002, DB: db, LastModelID: 1}

	_, err := SendRequest(context.Background(), session,
		func(string, string, uint64, structs.Usage, *string) error { return nil })
	if err == nil {
		t.Fatal("expected build/placeholder failure for missing chat row")
	}
	if session.State != state.StateIdle {
		t.Fatalf("build failure left State=%v, want Idle (err=%v)", session.State, err)
	}
	if session.ToolState != 0 {
		t.Fatalf("build failure left ToolState=%d, want 0", session.ToolState)
	}
}

// TestP118SendRequestTransportErrorResetsState 请求发送失败（连接被拒）时，
// 状态必须从 Requesting 复位到 Idle。修复前停在 StateRequesting。
func TestP118SendRequestTransportErrorResetsState(t *testing.T) {
	ensureTestConfig(t, 1, cfgStruct.ModelConfig{ModelName: "m", ModelID: "m", ProviderURL: "http://127.0.0.1:1", ProviderKey: "k"})
	session, cleanup := newStateTestSession(t, 1)
	defer cleanup()
	if err := session.DB.Create(&storageStructs.Messages{
		ChatID: session.ID, Type: storageStructs.MessagesRoleUser, Delta: "hi",
	}).Error; err != nil {
		t.Fatalf("create user message: %v", err)
	}

	_, err := SendRequest(context.Background(), session,
		func(string, string, uint64, structs.Usage, *string) error { return nil })
	if err == nil {
		t.Fatal("expected transport error for unreachable provider")
	}
	if session.State != state.StateIdle {
		t.Fatalf("transport error left State=%v, want Idle", session.State)
	}
}

// TestP118SendRequestSuccessNoToolsResetsState 正常完成且无工具调用时，
// 状态必须回到 Idle。修复前没有任何路径复位，State 停在 Requesting/Reciving。
func TestP118SendRequestSuccessNoToolsResetsState(t *testing.T) {
	openai.StartServerTask()
	ensureTestConfig(t, 1, cfgStruct.ModelConfig{
		ModelName: "test-chat-flash", ModelID: "test-chat-flash",
		ProviderURL: openai.BaseURL, ProviderKey: "k",
	})
	session, cleanup := newStateTestSession(t, 1)
	defer cleanup()
	if err := session.DB.Create(&storageStructs.Messages{
		ChatID: session.ID, Type: storageStructs.MessagesRoleUser, Delta: "hello",
	}).Error; err != nil {
		t.Fatalf("create user message: %v", err)
	}

	finish, err := SendRequest(context.Background(), session,
		func(string, string, uint64, structs.Usage, *string) error { return nil })
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if !finish {
		t.Fatal("expected finish=true for a text-only response")
	}
	if session.State != state.StateIdle {
		t.Fatalf("successful text-only response left State=%v, want Idle", session.State)
	}
}

// TestP118SendRequestCancelResetsState 用户取消后状态必须回到 Idle。
func TestP118SendRequestCancelResetsState(t *testing.T) {
	openai.StartServerTask()
	ensureTestConfig(t, 1, cfgStruct.ModelConfig{
		ModelName: "test-chat", ModelID: "test-chat",
		ProviderURL: openai.BaseURL, ProviderKey: "k",
	})
	session, cleanup := newStateTestSession(t, 1)
	defer cleanup()
	if err := session.DB.Create(&storageStructs.Messages{
		ChatID: session.ID, Type: storageStructs.MessagesRoleUser, Delta: "hello",
	}).Error; err != nil {
		t.Fatalf("create user message: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := SendRequest(ctx, session,
			func(string, string, uint64, structs.Usage, *string) error { return nil })
		errCh <- err
	}()
	time.Sleep(250 * time.Millisecond) // test-chat 50ms/词，确保进入流式接收
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendRequest did not return after cancel")
	}
	if session.State != state.StateIdle {
		t.Fatalf("cancel left State=%v, want Idle", session.State)
	}
}

// TestP118ExecuteToolCallsErrorResetsState 工具执行前的校验失败（同轮两个生命周期
// 调用）必须把 WaitApprove 复位到 Idle。修复前停在 WaitApprove。
func TestP118ExecuteToolCallsErrorResetsState(t *testing.T) {
	session, cleanup := newStateTestSession(t, 1)
	defer cleanup()
	session.State = state.StateWaitApprove

	payload := `[{"name":"activate_agent","id":"a","parameters":{}},{"name":"deactivate_agent","id":"b","parameters":{}}]`
	_, err := ExecuteToolCalls(session, payload)
	if err == nil {
		t.Fatal("expected lifecycle validation error")
	}
	if session.State != state.StateIdle {
		t.Fatalf("ExecuteToolCalls error left State=%v, want Idle", session.State)
	}
}

// === 标题覆盖（整行 Save -> 单列更新） ===

// TestP118WaitApproveRejectDoesNotClobberAITitle 内存中的 AITitle 为空（旧快照）时，
// WaitApprove 下处理用户输入不得把 DB 中已生成的 ai_title 覆盖回空串。
func TestP118WaitApproveRejectDoesNotClobberAITitle(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	if config.GlobalConfig == nil {
		config.GlobalConfig = &cfgStruct.Config{}
	}
	config.GlobalConfig.Agent.DisablePromptPreprocess = true

	chat := storageStructs.Chats{ID: 9100}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	// 标题 goroutine 已异步写入 DB
	if err := db.Model(&storageStructs.Chats{}).Where("id = ?", chat.ID).
		Update("ai_title", "AI 生成的标题").Error; err != nil {
		t.Fatalf("update ai_title: %v", err)
	}
	// 会话内存快照仍是旧值（空标题）
	session := &storageStructs.Chats{ID: chat.ID, DB: db, State: state.StateWaitApprove}
	if _, err := UserAddMsgWithID(session, "换个思路", nil); err != nil {
		t.Fatalf("UserAddMsgWithID: %v", err)
	}
	var got storageStructs.Chats
	if err := db.First(&got, chat.ID).Error; err != nil {
		t.Fatalf("reload chat: %v", err)
	}
	if got.AITitle != "AI 生成的标题" {
		t.Fatalf("ai_title was clobbered: got %q, want %q", got.AITitle, "AI 生成的标题")
	}
	if got.State != state.StateIdle {
		t.Fatalf("state after reject = %v, want Idle", got.State)
	}
}

// === 摘要请求可取消（provider 侧契约，ui/loop 的 Stop() 依赖它）===

// TestP118SummaryHonorsContextCancel 摘要网络请求必须响应 context 取消。
func TestP118SummaryHonorsContextCancel(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	started := make(chan struct{})
	var startOnce sync.Once
	release := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	var handlerWG sync.WaitGroup
	handlerWG.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
		case <-release:
		}
		handlerWG.Done()
	}))
	defer func() {
		closeRelease()
		handlerWG.Wait()
		srv.Close()
	}()

	ensureTestConfig(t, 1, cfgStruct.ModelConfig{
		ModelName: "summary-model", ModelID: "summary-model",
		ProviderURL: srv.URL, ProviderKey: "k",
	})
	config.GlobalConfig.Agent.SummaryModel = 1

	chat := storageStructs.Chats{ID: 9300}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	// 需要 > summaryKeepNumber(6) 条消息才会产生非 0 的 lastMsgID
	for i := range 8 {
		if err := db.Create(&storageStructs.Messages{
			ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "消息" + string(rune('A'+i)),
		}).Error; err != nil {
			t.Fatalf("create message: %v", err)
		}
	}
	session := &storageStructs.Chats{ID: chat.ID, DB: db, LastModelID: 1}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := SummarySession(ctx, session)
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("summary request was not sent")
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected SummarySession to fail after cancel")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SummarySession ignored context cancel")
	}
}
