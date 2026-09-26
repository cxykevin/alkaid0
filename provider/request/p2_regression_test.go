package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/provider/response"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// requestSystemContains 判断请求体的 system 消息中是否包含指定文本。
func requestSystemContains(req structs.ChatCompletionRequest, text string) bool {
	for _, m := range req.Messages {
		if m.Role == structs.RoleSystem && strings.Contains(m.Content, text) {
			return true
		}
	}
	return false
}

// requestContainsAnywhere 判断请求体的任意消息（system / user / tool）中是否包含指定文本。
// 运行期内部通知（后台任务结束 / shell 停止等）现在注入**消息列表末尾**的独立块，
// 而不是 system 消息——system 消息排在 tools 之后，任何一次通知变动都会把 tools
// 之后的整个前缀（含全部历史）打掉，前缀缓存命中率会掉到 tools 前缀大小。
// 因此"通知是否随请求发出"要按整份请求体判断，"不许进 system"另行断言。
func requestContainsAnywhere(req structs.ChatCompletionRequest, text string) bool {
	for _, m := range req.Messages {
		if strings.Contains(m.Content, text) {
			return true
		}
	}
	return false
}

// TestSendRequest_RetryKeepsQueuedSystemNotice 复现 P2-1：首次请求失败后 ui/loop 会
// 重新调用 SendRequest，重试请求必须仍带着排队的 system 通知。
// 此前 build.Build 在构建时就把 session.SystemPrompt 清空，重试请求不再包含通知。
// 通知的**落位**随后改为消息列表末尾（缓存优化，见 build.RequestBody 的注释），
// 但"重试不丢通知"这一契约不变：两次请求都必须带着它，且都不许塞进 system 消息。
func TestSendRequest_RetryKeepsQueuedSystemNotice(t *testing.T) {
	initAgentsConsumer()

	var mu sync.Mutex
	var bodies []structs.ChatCompletionRequest
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var req structs.ChatCompletionRequest
		_ = json.Unmarshal(payload, &req)
		mu.Lock()
		bodies = append(bodies, req)
		attempt++
		n := attempt
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			// 首次请求失败：模拟临时 500，调用方（ui/loop）会重试
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("temporary failure"))
			return
		}
		emitTextSSE(w, "retry ok")
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{ID: 9101, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "hello"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}

	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     true,
		EnableScopes:   make(map[string]bool),
	}
	session.SystemPrompt = "[System] background shell make stopped"

	if _, err := SendRequest(context.Background(), session, noopCallback); err == nil {
		t.Fatal("首次请求应失败（500），实际返回 nil")
	}
	// 模拟 ui/loop 的重试
	if _, err := SendRequest(context.Background(), session, noopCallback); err != nil {
		t.Fatalf("重试 SendRequest 失败: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("期望 2 次请求，实际 %d 次", len(bodies))
	}
	for i, b := range bodies {
		if !requestContainsAnywhere(b, "background shell") {
			t.Errorf("第 %d 次请求丢失了排队的 system 通知（重试丢失）", i+1)
		}
		if requestSystemContains(b, "background shell") {
			t.Errorf("第 %d 次请求把运行期通知放进了 system 消息：会打掉 tools 之后的整个前缀缓存", i+1)
		}
	}
}

// TestSimpleOpenAIRequest_ClientTimeoutIsNotUserCancel 复现 P2-2：
// httpClient 自带的 120s 超时（读流 / 等响应头）必须与用户主动取消区分开。
// 修复前超时错误包裹 context.DeadlineExceeded，ui/loop 会据此上报 StopReasonUser 并跳过重试。
func TestSimpleOpenAIRequest_ClientTimeoutIsNotUserCancel(t *testing.T) {
	withShortClientTimeout := func(t *testing.T, d time.Duration) {
		old := httpClient.Timeout
		httpClient.Timeout = d
		t.Cleanup(func() { httpClient.Timeout = old })
	}

	t.Run("streaming body read", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		}))
		defer srv.Close()
		withShortClientTimeout(t, 200*time.Millisecond)

		err := SimpleOpenAIRequest(context.Background(), srv.URL, "k", "m",
			structs.ChatCompletionRequest{Messages: []structs.Message{{Role: structs.RoleUser, Content: "hi"}}},
			nil, func(structs.ChatCompletionResponse) error { return nil })
		assertClientTimeout(t, err)
	})

	t.Run("awaiting headers", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		withShortClientTimeout(t, 200*time.Millisecond)

		err := SimpleOpenAIRequest(context.Background(), srv.URL, "k", "m",
			structs.ChatCompletionRequest{Messages: []structs.Message{{Role: structs.RoleUser, Content: "hi"}}},
			nil, func(structs.ChatCompletionResponse) error { return nil })
		assertClientTimeout(t, err)
	})
}

// assertClientTimeout 断言错误代表"客户端自身超时"，而不是用户停止/上层 context 超时。
func assertClientTimeout(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("期望超时错误，实际为 nil")
	}
	t.Logf("超时错误: %v", err)
	if errors.Is(err, context.Canceled) {
		t.Fatalf("客户端超时被误判为用户取消: %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("客户端超时被包裹成 context.DeadlineExceeded，ui/loop 会误报 StopReasonUser: %v", err)
	}
	if !errors.Is(err, ErrRequestTimeout) {
		t.Fatalf("期望 ErrRequestTimeout，实际 %v", err)
	}
}

// TestSimpleOpenAIRequest_UserCancelStaysCanceled 回归保护：用户主动取消仍须是
// context.Canceled（不能被超时分类逻辑吞掉）。
func TestSimpleOpenAIRequest_UserCancelStaysCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := SimpleOpenAIRequest(ctx, srv.URL, "k", "m",
		structs.ChatCompletionRequest{Messages: []structs.Message{{Role: structs.RoleUser, Content: "hi"}}},
		nil, func(structs.ChatCompletionResponse) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("用户取消应返回 context.Canceled，实际 %v", err)
	}
}

// TestSendRequest_DoneTokenErrorPersistsUnflushedTail 复现 P2-3：收尾（DoneToken）
// 报错时，未达刷写阈值（256 字符）的流式正文和 DoneToken 返回的尾部都必须先落库。
// 通过 solverDoneToken seam 注入收尾错误，保证用例与 provider/parser 的改动解耦。
func TestSendRequest_DoneTokenErrorPersistsUnflushedTail(t *testing.T) {
	initAgentsConsumer()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 未达刷写阈值的正文
		emitTextSSE(w, "unflushed tail content")
		sseChunk(w, structs.ChatCompletionResponse{
			ID: "chatcmpl-p2", Model: "native-e2e",
			Choices: []structs.Choice{{Index: 0, Delta: structs.Message{}, FinishReason: "stop"}},
		})
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := storageStructs.Chats{ID: 9102, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "start"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     true,
		EnableScopes:   make(map[string]bool),
	}

	// 注入收尾错误，并让 DoneToken 返回一段尾部增量（模拟解析器收尾失败"丢弃尾部"）
	doneTokenErr := errors.New("injected DoneToken failure")
	oldDoneToken := solverDoneToken
	solverDoneToken = func(*response.Solver) (bool, string, string, error) {
		return true, " tail-from-done-token", "", doneTokenErr
	}
	t.Cleanup(func() { solverDoneToken = oldDoneToken })

	_, err := SendRequest(context.Background(), session, noopCallback)
	if !errors.Is(err, doneTokenErr) {
		t.Fatalf("注入的收尾错误应向上返回，实际 %v", err)
	}

	var saved storageStructs.Messages
	if err := db.First(&saved, session.CurrentMessageID).Error; err != nil {
		t.Fatalf("读取 assistant 占位消息失败: %v", err)
	}
	if !strings.Contains(saved.Delta, "unflushed tail content") {
		t.Fatalf("DoneToken 报错时未 flush 的正文丢失: Delta=%q", saved.Delta)
	}
	if !strings.Contains(saved.Delta, "tail-from-done-token") {
		t.Fatalf("DoneToken 报错时其返回的尾部增量丢失: Delta=%q", saved.Delta)
	}
}

// TestSendRequest_CancelBeforeAnyContentLeavesNoEmptyAssistantRow 复现 P2-4：
// 取消时若尚未收到任何内容，占位 assistant 行必须删除，否则会被 summary/历史回放带上。
func TestSendRequest_CancelBeforeAnyContentLeavesNoEmptyAssistantRow(t *testing.T) {
	initAgentsConsumer()

	requestArrived := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(requestArrived)
		<-r.Context().Done()
	}))
	defer srv.Close()
	setupNativeE2EConfig(srv.URL)

	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	// 内存库固定单连接：SendRequest 在另一 goroutine 用库，避免连接池另开一个空内存库
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}

	chat := storageStructs.Chats{ID: 9103, LastModelID: 1}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&storageStructs.Messages{ChatID: chat.ID, Type: storageStructs.MessagesRoleUser, Delta: "cancel me"}).Error; err != nil {
		t.Fatalf("create user msg: %v", err)
	}
	session := &storageStructs.Chats{
		ID:             chat.ID,
		DB:             db,
		LastModelID:    1,
		CurrentAgentID: "",
		InTestFlag:     true,
		EnableScopes:   make(map[string]bool),
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := SendRequest(ctx, session, noopCallback)
		errCh <- err
	}()

	// 等 HTTP 请求真正发出（占位行在此之前已创建），再取消
	select {
	case <-requestArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("请求未发出")
	}

	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("期望 context.Canceled，实际 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后 SendRequest 未返回")
	}

	var n int64
	if err := db.Model(&storageStructs.Messages{}).
		Where("chat_id = ? AND type = ?", chat.ID, storageStructs.MessagesRoleAgent).Count(&n).Error; err != nil {
		t.Fatalf("count agent messages: %v", err)
	}
	if n != 0 {
		t.Fatalf("取消后残留 %d 条空 assistant 行（summary/历史回放会带上空消息）", n)
	}
}
