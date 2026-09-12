package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/loop"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// registerTestSessionByID 按完整 sessionID 注册测试会话。
// sessionID2Cwd 现在从会话注册表读取真实 cwd，测试中需先注册会话。
func registerTestSessionByID(t *testing.T, sessionID, cwd string, id uint32) {
	t.Helper()
	sessLock.Lock()
	sessions[sessionID] = &sessionObj{
		cwd: cwd,
		id:  id,
		ctx: context.Background(),
	}
	sessLock.Unlock()
	t.Cleanup(func() {
		sessLock.Lock()
		delete(sessions, sessionID)
		sessLock.Unlock()
	})
}

// registerTestSession 按目录+ID 注册测试会话并返回规范 sessionID
func registerTestSession(t *testing.T, cwd string, id uint32) string {
	t.Helper()
	sessID := cwd2SessionID(cwd, id)
	registerTestSessionByID(t, sessID, cwd, id)
	return sessID
}

// TestSessionID2Cwd 测试会话ID解析功能
func TestSessionID2Cwd(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantCwd   string
		wantID    uint32
		wantErr   bool
	}{
		{
			name:      "有效的会话ID",
			sessionID: "sess_123:/tmp/test",
			wantCwd:   "/tmp/test",
			wantID:    123,
			wantErr:   false,
		},
		{
			name:      "会话ID过短",
			sessionID: "sess_",
			wantErr:   true,
		},
		{
			name:      "会话ID格式错误无冒号",
			sessionID: "sess_123tmp",
			wantErr:   true,
		},
		{
			name:      "会话ID数字解析失败",
			sessionID: "sess_abc:/tmp/test",
			wantErr:   true,
		},
		{
			name:      "包含多个冒号",
			sessionID: "sess_123:/tmp:test",
			wantCwd:   "/tmp:test",
			wantID:    123,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// sessionID2Cwd 现在从会话注册表读取，成功用例需先注册会话
			if !tt.wantErr {
				registerTestSessionByID(t, tt.sessionID, tt.wantCwd, tt.wantID)
			}
			cwd, id, err := sessionID2Cwd(tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("sessionID2Cwd() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if cwd != tt.wantCwd {
					t.Errorf("sessionID2Cwd() cwd = %v, want %v", cwd, tt.wantCwd)
				}
				if id != tt.wantID {
					t.Errorf("sessionID2Cwd() id = %v, want %v", id, tt.wantID)
				}
			}
		})
	}
}

// TestCwd2SessionID 测试会话ID生成功能
func TestCwd2SessionID(t *testing.T) {
	tests := []struct {
		name     string
		cwd      string
		id       uint32
		wantResp string
	}{
		{
			name:     "基础会话ID生成",
			cwd:      "/tmp/test",
			id:       123,
			wantResp: "sess_123:/tmp/test",
		},
		{
			name:     "ID为0",
			cwd:      "/home/user",
			id:       0,
			wantResp: "sess_0:/home/user",
		},
		{
			name:     "大ID值",
			cwd:      "/path",
			id:       4294967295,
			wantResp: "sess_4294967295:/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := cwd2SessionID(tt.cwd, tt.id)
			if resp != tt.wantResp {
				t.Errorf("cwd2SessionID() = %v, want %v", resp, tt.wantResp)
			}
		})
	}
}

// TestToolNameToType 测试工具名称类型映射
func TestToolNameToType(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		wantType string
	}{
		{"agent映射", "agent", "other"},
		{"edit映射", "edit", "edit"},
		{"run映射", "run", "execute"},
		{"trace映射", "trace", "read"},
		{"unkn映射使用默认", "unknown", "other"}, // 通过Default处理
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, ok := ToolNameToTypeMap[tt.toolName]
			if !ok {
				typ = "other" // 模拟Default行为
			}
			if typ != tt.wantType {
				t.Errorf("ToolNameToType[%s] = %v, want %v", tt.toolName, typ, tt.wantType)
			}
		})
	}
}

func TestSessionNewHidden(t *testing.T) {
	tests := []struct {
		name string
		args map[string]json.RawMessage
		want bool
	}{
		{name: "true", args: map[string]json.RawMessage{sessionNewHiddenArg: json.RawMessage(`true`)}, want: true},
		{name: "false", args: map[string]json.RawMessage{sessionNewHiddenArg: json.RawMessage(`false`)}},
		{name: "null", args: map[string]json.RawMessage{sessionNewHiddenArg: json.RawMessage(`null`)}},
		{name: "string", args: map[string]json.RawMessage{sessionNewHiddenArg: json.RawMessage(`"true"`)}},
		{name: "number", args: map[string]json.RawMessage{sessionNewHiddenArg: json.RawMessage(`1`)}},
		{name: "object", args: map[string]json.RawMessage{sessionNewHiddenArg: json.RawMessage(`{}`)}},
		{name: "array", args: map[string]json.RawMessage{sessionNewHiddenArg: json.RawMessage(`[]`)}},
		{name: "meta is ignored", args: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionNewHidden(tt.args); got != tt.want {
				t.Errorf("sessionNewHidden() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSessionNewValidation 测试SessionNew的参数验证
func TestSessionNewValidation(t *testing.T) {
	tests := []struct {
		name        string
		cwd         string
		wantErr     bool
		errContains string
	}{
		{
			name:        "空的cwd",
			cwd:         "",
			wantErr:     true,
			errContains: "cwd is empty",
		},
		{
			name:        "不存在的目录",
			cwd:         "/nonexistent/path/12345",
			wantErr:     true,
			errContains: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SessionNew(SessionNewRequest{Cwd: tt.cwd}, nil, 1)
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionNew() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !contains(err.Error(), tt.errContains) {
				t.Errorf("SessionNew() error message = %v, want contains %v", err.Error(), tt.errContains)
			}
		})
	}
}

// TestSessionListValidation 测试SessionList的参数验证
func TestSessionListValidation(t *testing.T) {
	tests := []struct {
		name        string
		cwd         string
		wantErr     bool
		errContains string
	}{
		{
			name:        "不存在的工作目录",
			cwd:         "/nonexistent/path/98765",
			wantErr:     true,
			errContains: "not found",
		},
		{
			name:        "未初始化的工作目录",
			cwd:         t.TempDir(),
			wantErr:     true,
			errContains: "not inited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SessionList(SessionListRequest{Cwd: tt.cwd}, nil, 1)
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !contains(err.Error(), tt.errContains) {
				t.Errorf("SessionList() error message = %v, want contains %v", err.Error(), tt.errContains)
			}
		})
	}
}

// newSessionListDB 创建带 .alkaid0 的临时目录并创建 n 个会话。
// 返回 (目录, db 句柄, 会话 ID 列表)。db 句柄引用计数由 t.Cleanup 平衡。
func newSessionListDB(t *testing.T, n int) (string, *gorm.DB, []uint32) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("failed to create .alkaid0: %v", err)
	}
	db, err := loadDB(tmpDir)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	ids := make([]uint32, n)
	for i := range n {
		chat := structs.Chats{Title: fmt.Sprintf("Chat %d", i)}
		if err := db.Create(&chat).Error; err != nil {
			t.Fatalf("failed to create chat %d: %v", i, err)
		}
		ids[i] = chat.ID
	}
	t.Cleanup(func() { closeDB(tmpDir) })
	return tmpDir, db, ids
}

// setChatUpdatedAt 通过原生 SQL 覆写会话的 updated_at（绕过 GORM 的 autoUpdateTime 回调）。
func setChatUpdatedAt(t *testing.T, db *gorm.DB, id uint32, at time.Time) {
	t.Helper()
	if err := db.Exec("UPDATE chats SET updated_at = ? WHERE id = ?", at, id).Error; err != nil {
		t.Fatalf("failed to set updated_at for chat %d: %v", id, err)
	}
}

// TestSessionList_OrderAndUpdatedAt 验证 session/list 按最后活动时间倒序并返回 updatedAt
func TestSessionList_OrderAndUpdatedAt(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 3)
	// 设置递增的最后活动时间：id 越大越新
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for i, id := range ids {
		setChatUpdatedAt(t, db, id, base.Add(time.Duration(i)*time.Hour))
	}

	resp, err := SessionList(SessionListRequest{Cwd: dir}, nil, 1)
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	if len(resp.Sessions) != len(ids) {
		t.Fatalf("expected %d sessions, got %d", len(ids), len(resp.Sessions))
	}
	for i := range ids {
		wantID := ids[len(ids)-1-i]
		got := resp.Sessions[i]
		if got.SessionID != cwd2SessionID(dir, wantID) {
			t.Errorf("session[%d] mismatch: got %s, want id %d", i, got.SessionID, wantID)
		}
		wantAt := base.Add(time.Duration(len(ids)-1-i) * time.Hour).UTC()
		parsed, err := time.Parse(time.RFC3339, got.UpdatedAt)
		if err != nil {
			t.Fatalf("updatedAt %q not RFC3339: %v", got.UpdatedAt, err)
		}
		if !parsed.Equal(wantAt) {
			t.Errorf("session[%d] updatedAt mismatch: got %s, want %s",
				i, got.UpdatedAt, wantAt.Format(time.RFC3339))
		}
	}
	// 会话数不足一页时无 nextCursor
	if resp.NextCursor != "" {
		t.Errorf("expected no nextCursor, got %q", resp.NextCursor)
	}
}

// TestSessionList_HiddenChats 验证隐藏会话不出现在列表，且不占用分页容量。
func TestSessionList_HiddenChats(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 3)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	for i, id := range ids {
		setChatUpdatedAt(t, db, id, base.Add(time.Duration(i)*time.Hour))
	}
	if err := db.Model(&structs.Chats{}).Where("id = ?", ids[1]).Update("hidden", true).Error; err != nil {
		t.Fatalf("failed to hide chat: %v", err)
	}

	resp, err := SessionList(SessionListRequest{Cwd: dir}, nil, 1)
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("expected 2 visible sessions, got %d", len(resp.Sessions))
	}
	wantIDs := []uint32{ids[2], ids[0]}
	for i, id := range wantIDs {
		if got := resp.Sessions[i].SessionID; got != cwd2SessionID(dir, id) {
			t.Errorf("session[%d] = %s, want id %d", i, got, id)
		}
	}
	if resp.NextCursor != "" {
		t.Errorf("expected no nextCursor, got %q", resp.NextCursor)
	}

	found, err := funcs.QueryChat(db, ids[1])
	if err != nil {
		t.Fatalf("QueryChat failed for hidden chat: %v", err)
	}
	if !found.Hidden {
		t.Error("QueryChat did not preserve hidden flag")
	}
}

// TestSessionList_AllHidden 验证全部会话隐藏时列表为空且没有游标。
func TestSessionList_AllHidden(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 3)
	if err := db.Model(&structs.Chats{}).Where("id IN ?", ids).Update("hidden", true).Error; err != nil {
		t.Fatalf("failed to hide chats: %v", err)
	}

	resp, err := SessionList(SessionListRequest{Cwd: dir}, nil, 1)
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	if len(resp.Sessions) != 0 {
		t.Fatalf("expected no visible sessions, got %d", len(resp.Sessions))
	}
	if resp.NextCursor != "" {
		t.Errorf("expected no nextCursor, got %q", resp.NextCursor)
	}
}

// TestSessionList_Pagination 验证 cursor 分页：每页最多 30 条、跨页不重不漏、顺序全局倒序。
func TestSessionList_Pagination(t *testing.T) {
	const total = 75 // 30 + 30 + 15
	dir, db, ids := newSessionListDB(t, total)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range ids {
		setChatUpdatedAt(t, db, id, base.Add(time.Duration(i)*time.Minute))
	}

	var all []string
	cursor := ""
	pages := 0
	for {
		resp, err := SessionList(SessionListRequest{Cwd: dir, Cursor: cursor}, nil, 1)
		if err != nil {
			t.Fatalf("SessionList page %d failed: %v", pages, err)
		}
		if len(resp.Sessions) > 30 {
			t.Fatalf("page %d has %d sessions, want <= 30", pages, len(resp.Sessions))
		}
		for _, s := range resp.Sessions {
			all = append(all, s.SessionID)
		}
		pages++
		if resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
	if pages != 3 {
		t.Errorf("expected 3 pages, got %d", pages)
	}
	if len(all) != total {
		t.Fatalf("expected %d sessions across pages, got %d", total, len(all))
	}
	// 期望顺序：ids 倒序（updated_at 随创建顺序递增）
	for i, sid := range all {
		wantID := ids[total-1-i]
		if sid != cwd2SessionID(dir, wantID) {
			t.Fatalf("session %d out of order: got %s, want id %d", i, sid, wantID)
		}
	}
	// 跨页无重复
	seen := map[string]bool{}
	for _, sid := range all {
		if seen[sid] {
			t.Fatalf("duplicate session %s across pages", sid)
		}
		seen[sid] = true
	}
}

// TestSessionList_InvalidCursor 无效 cursor 应返回错误（ACP 规范）
func TestSessionList_InvalidCursor(t *testing.T) {
	dir, _, _ := newSessionListDB(t, 2)
	_, err := SessionList(SessionListRequest{Cwd: dir, Cursor: "not-a-valid-cursor"}, nil, 1)
	if err == nil || !contains(err.Error(), "invalid cursor") {
		t.Errorf("expected invalid cursor error, got %v", err)
	}
}

// TestSessionList_LegacyZeroUpdatedAt 历史数据（updated_at 为零值）应排序垫底且 updatedAt 显示为 Unix 纪元
func TestSessionList_LegacyZeroUpdatedAt(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 3)
	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	setChatUpdatedAt(t, db, ids[1], base.Add(time.Hour))
	setChatUpdatedAt(t, db, ids[2], base)
	// 显式清空 ids[0] 的时间，模拟未记录 updated_at 的旧会话
	if err := db.Exec("UPDATE chats SET updated_at = NULL WHERE id = ?", ids[0]).Error; err != nil {
		t.Fatalf("failed to clear updated_at for chat %d: %v", ids[0], err)
	}

	resp, err := SessionList(SessionListRequest{Cwd: dir}, nil, 1)
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	if len(resp.Sessions) != len(ids) {
		t.Fatalf("expected %d sessions, got %d", len(ids), len(resp.Sessions))
	}
	want := []uint32{ids[1], ids[2], ids[0]}
	for i, id := range want {
		got := resp.Sessions[i]
		if got.SessionID != cwd2SessionID(dir, id) {
			t.Errorf("session[%d] mismatch: got %s, want id %d", i, got.SessionID, id)
		}
	}
	if got, want := resp.Sessions[2].UpdatedAt, "1970-01-01T00:00:00Z"; got != want {
		t.Errorf("legacy session updatedAt = %q, want %q", got, want)
	}
}

// sessionInfoUpdatePayload 构造客户端发起的 session_info_update 请求负载
func sessionInfoUpdatePayload(t *testing.T, title *string) json.RawMessage {
	t.Helper()
	m := map[string]any{"sessionUpdate": "session_info_update"}
	if title != nil {
		m["title"] = *title
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("failed to marshal update: %v", err)
	}
	return b
}

// TestHandleSessionUpdate_RenameColdSession 冷会话（未 new/resume）重命名标题：
// 无需内存注册，按 sessionId 字符串解析 + DB 校验后落库
func TestHandleSessionUpdate_RenameColdSession(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 1)
	sessionID := cwd2SessionID(dir, ids[0])

	resp, err := HandleSessionUpdate(SessionUpdateRequest{
		SessionID: sessionID,
		Update:    sessionInfoUpdatePayload(t, new("Implement user authentication")),
	}, nil, 1)
	if err != nil {
		t.Fatalf("HandleSessionUpdate failed: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected empty response, got %v", resp)
	}

	// 校验 DB 落库
	var reloaded structs.Chats
	if err := db.First(&reloaded, ids[0]).Error; err != nil {
		t.Fatalf("failed to reload chat: %v", err)
	}
	if reloaded.Title != "Implement user authentication" {
		t.Errorf("title = %q, want %q", reloaded.Title, "Implement user authentication")
	}
	if reloaded.UpdatedAt.IsZero() {
		t.Error("updated_at should be set after rename")
	}
}

// TestHandleSessionUpdate_ClearTitle 空串标题清除用户标题（回退 AI 标题）
func TestHandleSessionUpdate_ClearTitle(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 1)
	sessionID := cwd2SessionID(dir, ids[0])

	// 先设置用户标题
	empty := ""
	if _, err := HandleSessionUpdate(SessionUpdateRequest{
		SessionID: sessionID,
		Update:    sessionInfoUpdatePayload(t, new("临时标题")),
	}, nil, 1); err != nil {
		t.Fatalf("set title failed: %v", err)
	}
	// 清除
	if _, err := HandleSessionUpdate(SessionUpdateRequest{
		SessionID: sessionID,
		Update:    sessionInfoUpdatePayload(t, &empty),
	}, nil, 1); err != nil {
		t.Fatalf("clear title failed: %v", err)
	}

	var reloaded structs.Chats
	if err := db.First(&reloaded, ids[0]).Error; err != nil {
		t.Fatalf("failed to reload chat: %v", err)
	}
	if reloaded.Title != "" {
		t.Errorf("title = %q, want empty after clear", reloaded.Title)
	}
}

// TestHandleSessionUpdate_NoTitleNoChange title 省略时不应改动标题
func TestHandleSessionUpdate_NoTitleNoChange(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 1)
	sessionID := cwd2SessionID(dir, ids[0])

	payload, _ := json.Marshal(map[string]any{"sessionUpdate": "session_info_update"})
	if _, err := HandleSessionUpdate(SessionUpdateRequest{SessionID: sessionID, Update: payload}, nil, 1); err != nil {
		t.Fatalf("HandleSessionUpdate failed: %v", err)
	}
	var reloaded structs.Chats
	if err := db.First(&reloaded, ids[0]).Error; err != nil {
		t.Fatalf("failed to reload chat: %v", err)
	}
	if reloaded.Title != "Chat 0" {
		t.Errorf("title = %q, want unchanged %q", reloaded.Title, "Chat 0")
	}
}

// TestHandleSessionUpdate_InvalidVariant 不支持的 sessionUpdate 变体应报错
func TestHandleSessionUpdate_InvalidVariant(t *testing.T) {
	dir, _, ids := newSessionListDB(t, 1)
	sessionID := cwd2SessionID(dir, ids[0])

	payload, _ := json.Marshal(map[string]any{"sessionUpdate": "state_update"})
	_, err := HandleSessionUpdate(SessionUpdateRequest{SessionID: sessionID, Update: payload}, nil, 1)
	if err == nil || !contains(err.Error(), "unsupported session update variant") {
		t.Errorf("expected unsupported variant error, got %v", err)
	}
}

// TestHandleSessionUpdate_SessionNotFound 无效会话 ID 应报错
func TestHandleSessionUpdate_SessionNotFound(t *testing.T) {
	_, err := HandleSessionUpdate(SessionUpdateRequest{
		SessionID: "invalid",
		Update:    sessionInfoUpdatePayload(t, new("x")),
	}, nil, 1)
	if err == nil || !contains(err.Error(), "session") {
		t.Errorf("expected session error, got %v", err)
	}
}

// TestHandleSessionUpdate_BroadcastAndSync 已加载会话：内存同步 + 广播 session_info_update
func TestHandleSessionUpdate_BroadcastAndSync(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 1)
	sessionID := cwd2SessionID(dir, ids[0])

	// 模拟会话已加载（带真实 session 对象）
	sessObj := &structs.Chats{ID: ids[0], Title: "", AITitle: "AI 标题", DB: db}
	sessions[sessionID] = &sessionObj{cwd: dir, id: ids[0], session: sessObj}
	t.Cleanup(func() {
		sessLock.Lock()
		delete(sessions, sessionID)
		sessLock.Unlock()
	})

	// 模拟已连接客户端捕获广播
	var mu sync.Mutex
	var got []SessionUpdate
	const connID uint64 = 77
	connCallLock.Lock()
	connCallMap[connID] = func(method string, update any, _ *string) error {
		if method != "session/update" {
			return nil
		}
		mu.Lock()
		if su, ok := update.(SessionUpdate); ok {
			got = append(got, su)
		}
		mu.Unlock()
		return nil
	}
	connCallLock.Unlock()
	sessionConnLock.Lock()
	sessionConnMap[sessionID] = []uint64{connID}
	sessionConnLock.Unlock()
	t.Cleanup(func() {
		connCallLock.Lock()
		delete(connCallMap, connID)
		connCallLock.Unlock()
		sessionConnLock.Lock()
		delete(sessionConnMap, sessionID)
		sessionConnLock.Unlock()
	})

	if _, err := HandleSessionUpdate(SessionUpdateRequest{
		SessionID: sessionID,
		Update:    sessionInfoUpdatePayload(t, new("新标题")),
	}, nil, connID); err != nil {
		t.Fatalf("HandleSessionUpdate failed: %v", err)
	}

	// 内存同步
	if sessObj.Title != "新标题" {
		t.Errorf("in-memory title = %q, want %q", sessObj.Title, "新标题")
	}
	if sessObj.UpdatedAt.IsZero() {
		t.Error("in-memory updated_at should be set")
	}

	// 广播内容
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 broadcast, got %d", n)
	}
	inner, ok := got[0].Update.(SessionUpdateUpdate)
	if !ok {
		t.Fatalf("unexpected update type %T", got[0].Update)
	}
	if inner.SessionUpdate != "session_info_update" || inner.Title != "新标题" {
		t.Errorf("broadcast mismatch: variant=%q title=%q", inner.SessionUpdate, inner.Title)
	}
	if inner.UpdatedAt == "" {
		t.Error("broadcast should carry updatedAt")
	}
}

// strPtr 返回字符串指针
//
//go:fix inline
func strPtr(s string) *string {
	return new(s)
}

// TestSessionResumeValidation 测试SessionResume的参数验证
func TestSessionResumeValidation(t *testing.T) {
	tests := []struct {
		name        string
		cwd         string
		sessionID   string
		wantErr     bool
		errContains string
	}{
		{
			name:        "无效的会话ID",
			cwd:         "/tmp",
			sessionID:   "invalid",
			wantErr:     true,
			errContains: "session not found",
		},
		{
			name:        "cwd不匹配",
			cwd:         "/tmp",
			sessionID:   "sess_123:/home/test",
			wantErr:     true,
			errContains: "not match",
		},
	}

	// 注册会话，使 "cwd不匹配" 用例能进入 cwd 比较逻辑（而非 "session not found"）
	registerTestSessionByID(t, "sess_123:/home/test", "/home/test", 123)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SessionResume(SessionResumeRequest{Cwd: tt.cwd, SessionID: tt.sessionID}, nil, 1)
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionResume() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !contains(err.Error(), tt.errContains) {
				t.Errorf("SessionResume() error message = %v, want contains %v", err.Error(), tt.errContains)
			}
		})
	}
}

// TestBindedSessionOnConnCleanup 测试连接关闭后的会话清理
func TestBindedSessionOnConnCleanup(t *testing.T) {
	// 重置全局状态
	bindedSessionOnConn = map[uint64][]string{}

	const connID uint64 = 12345

	// 模拟会话绑定
	bindedSessionOnConn[connID] = []string{"sess_1:/tmp", "sess_2:/tmp"}

	if len(bindedSessionOnConn[connID]) != 2 {
		t.Errorf("Expected 2 sessions bound to connection, got %d", len(bindedSessionOnConn[connID]))
	}

	// 模拟连接关闭（Close函数行为）
	delete(bindedSessionOnConn, connID)

	if _, ok := bindedSessionOnConn[connID]; ok {
		t.Errorf("Expected connection to be cleaned up, but it still exists")
	}
}

// TestSessionIDFormat 测试会话ID的正确格式
func TestSessionIDFormat(t *testing.T) {
	cwd := "/home/user/project"
	id := uint32(42)
	sessionID := cwd2SessionID(cwd, id)
	registerTestSession(t, cwd, id)

	// 验证往返转换
	parsedCwd, parsedID, err := sessionID2Cwd(sessionID)
	if err != nil {
		t.Errorf("Failed to parse session ID: %v", err)
		return
	}

	if parsedCwd != cwd {
		t.Errorf("cwd mismatch: got %s, want %s", parsedCwd, cwd)
	}

	if parsedID != id {
		t.Errorf("id mismatch: got %d, want %d", parsedID, id)
	}
}

// TestParseSessionID 测试纯字符串会话ID解析（session/load 冷还原专用）
func TestParseSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantCwd   string
		wantID    uint32
		wantErr   bool
	}{
		{name: "有效的会话ID", sessionID: "sess_123:/tmp/test", wantCwd: "/tmp/test", wantID: 123},
		{name: "空字符串", sessionID: "", wantErr: true},
		{name: "缺冒号", sessionID: "sess_123tmp", wantErr: true},
		{name: "缺sess_前缀", sessionID: "123:/tmp/test", wantErr: true},
		{name: "前缀后无数字", sessionID: "sess_:/tmp/test", wantErr: true},
		{name: "id非数字", sessionID: "sess_abc:/tmp/test", wantErr: true},
		{name: "id超出uint32", sessionID: "sess_4294967296:/tmp/test", wantErr: true},
		{name: "cwd为空", sessionID: "sess_123:", wantErr: true},
		{name: "cwd包含冒号", sessionID: "sess_123:/tmp:test", wantCwd: "/tmp:test", wantID: 123},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd, id, err := parseSessionID(tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSessionID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if cwd != tt.wantCwd {
					t.Errorf("parseSessionID() cwd = %v, want %v", cwd, tt.wantCwd)
				}
				if id != tt.wantID {
					t.Errorf("parseSessionID() id = %v, want %v", id, tt.wantID)
				}
			}
		})
	}
}

// TestSessionLoadColdRestore 冷还原：内存注册表为空（模拟服务器重启）时，
// session/load 应能从磁盘数据库恢复已存在的会话；不存在的会话应报错且不注册。
func TestSessionLoadColdRestore(t *testing.T) {
	// 备份并清空全局注册表，模拟服务器冷启动
	oldSessions := sessions
	oldDbs := dbs
	oldBinded := bindedSessionOnConn
	oldConnCall := connCallMap
	oldSessionConn := sessionConnMap
	sessions = map[string]*sessionObj{}
	dbs = map[string]*dbObj{}
	bindedSessionOnConn = map[uint64][]string{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	sessionConnMap = map[string][]uint64{}
	// 恢复全局注册表时锁必须与生产代码一致：sessions 用 sessLock，
	// dbs 用 dbLock（loadDB/closeDB 均持 dbLock）。此前 dbs 恢复误用 sessLock，
	// 与 loadSession 启动的异步索引 goroutine 里 closeDB 的 dbLock 读冲突，
	// 触发偶发 data race（TestSessionReleaseTimerFires 下偶现）。
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		dbLock.Lock()
		dbs = oldDbs
		dbLock.Unlock()
		bindedSessionOnConn = oldBinded
		connCallMap = oldConnCall
		sessionConnMap = oldSessionConn
	}()

	// buildModelList / buildConfigOptions 依赖 config.GlobalConfig
	if config.GlobalConfig == nil {
		config.GlobalConfigSwap(cfgStructs.Config{})
	}

	tmpDir := t.TempDir()
	// loadSession 的异步索引 goroutine 会打开 codebase.sqlite 且数据库连接常驻，
	// Windows 上 TempDir 自动清理时无法删除被占用文件，导致测试误报 FAIL。
	// 注册清理在目录删除前关闭该目录的 codebase 数据库。
	// 注意：异步 goroutine 未完成时 CloseDirectory 后可能被其后续步骤重开，
	// 因此需先在"存在的会话可冷还原"子测试中等待 obj.indexDone。
	t.Cleanup(func() {
		_ = codebase.CloseDirectory(tmpDir)
	})
	db, err := storage.InitStorage(path.Join(tmpDir, ".alkaid0"), "")
	if err != nil {
		t.Fatalf("InitStorage failed: %v", err)
	}
	// InitStorage 打开的 db.sqlite 连接须在 TempDir 清理前关闭，
	// 否则 Windows 上无法删除被占用文件（与 codebase.sqlite 同类问题）。
	defer u.Unwrap(db.DB()).Close()
	id, err := funcs.CreateChat(db)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	sessionID := cwd2SessionID(tmpDir, id)

	// 安全的 call 接收函数（SessionLoad 会用它做历史回放与广播）
	call := func(string, any, *string) error { return nil }

	t.Run("存在的会话可冷还原", func(t *testing.T) {
		_, err := SessionResume(SessionResumeRequest{Cwd: tmpDir, SessionID: sessionID}, call, 1)
		if err != nil {
			t.Fatalf("cold restore SessionResume failed: %v", err)
		}
		sessLock.Lock()
		obj, ok := sessions[sessionID]
		sessLock.Unlock()
		if !ok {
			t.Error("session should be registered after cold restore")
		}
		// 等待 loadSession 的异步索引 goroutine（RunIndex + indexTempfsAndChatHistory）
		// 完成，避免其在主测试 t.Cleanup 关闭 codebase 后重开 codebase.sqlite，
		// 导致 Windows 上 TempDir 清理无法删除被占用文件。
		if ok && obj.indexDone != nil {
			select {
			case <-obj.indexDone:
			case <-time.After(10 * time.Second):
				t.Error("timeout waiting for async index goroutine")
			}
		}
		closeSession(sessionID)
	})

	t.Run("不存在的会话ID报错且不注册", func(t *testing.T) {
		missingSessionID := cwd2SessionID(tmpDir, id+1000)
		_, err := SessionResume(SessionResumeRequest{Cwd: tmpDir, SessionID: missingSessionID}, call, 1)
		if err == nil {
			t.Fatal("SessionResume with nonexistent session should fail")
		}
		sessLock.Lock()
		_, ok := sessions[missingSessionID]
		sessLock.Unlock()
		if ok {
			t.Error("nonexistent session should not be registered")
		}
	})
}

// TestSessionSetConfigOptionValidation 测试SessionSetConfigOption的参数验证
func TestSessionSetConfigOptionValidation(t *testing.T) {
	tests := []struct {
		name        string
		sessionID   string
		configID    string
		value       string
		wantErr     bool
		errContains string
	}{
		{
			name:        "空的sessionId",
			sessionID:   "",
			configID:    "model",
			value:       "0/model1",
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "空的configId",
			sessionID:   "sess_123:/tmp",
			configID:    "",
			value:       "0/model1",
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "空的value",
			sessionID:   "sess_123:/tmp",
			configID:    "model",
			value:       "",
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "无效的sessionId格式",
			sessionID:   "invalid",
			configID:    "model",
			value:       "0/model1",
			wantErr:     true,
			errContains: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SessionSetConfigOption(SessionSetConfigOptionRequest{
				SessionID: tt.sessionID,
				ConfigID:  tt.configID,
				Value:     tt.value,
			}, nil, 1)
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionSetConfigOption() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !contains(err.Error(), tt.errContains) {
				t.Errorf("SessionSetConfigOption() error message = %v, want contains %v", err.Error(), tt.errContains)
			}
		})
	}
}

// --- 延迟释放定时器测试 ---

// TestCancelSessionReleaseNonExistent 取消不存在的会话不应 panic
func TestCancelSessionReleaseNonExistent(t *testing.T) {
	cancelSessionRelease("nonexistent_session_id_12345")
}

// TestScheduleSessionReleaseNonExistent 调度不存在的会话不应 panic
func TestScheduleSessionReleaseNonExistent(t *testing.T) {
	scheduleSessionRelease("nonexistent_session_id_12345")
}

// TestSessionReleaseTimerCancel 调度释放后取消，会话应保留
func TestSessionReleaseTimerCancel(t *testing.T) {
	// 保存并清理全局状态
	oldSessions := sessions
	oldAgentCallList := agentCallList
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
	}()

	obj := &sessionObj{
		cwd:  "/tmp/test_release_cancel",
		id:   99991,
		loop: loop.New(nil),
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 调度释放
	scheduleSessionRelease(sessionID)

	// 立即取消
	cancelSessionRelease(sessionID)

	// 确认会话仍在
	sessLock.Lock()
	_, ok := sessions[sessionID]
	sessLock.Unlock()
	if !ok {
		t.Error("session should still exist after cancelSessionRelease")
	}

	// 确认定时器已清除
	if obj.releaseTimer != nil {
		t.Error("releaseTimer should be nil after cancelSessionRelease")
	}
}

// TestSessionReleaseTimerFires 调度释放后超时，会话应被清理
func TestSessionReleaseTimerFires(t *testing.T) {
	// 保存并清理全局状态
	oldSessions := sessions
	oldAgentCallList := agentCallList
	oldSessionTimeout := config.GlobalConfig.Server.SessionTimeout
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	config.GlobalConfig.Server.SessionTimeout = 1 // 1 秒超时
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
		config.GlobalConfig.Server.SessionTimeout = oldSessionTimeout
	}()

	obj := &sessionObj{
		cwd:  "/tmp/test_release_fires",
		id:   99992,
		loop: loop.New(nil),
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 调度释放（1 秒后触发）
	scheduleSessionRelease(sessionID)

	// 等待超时
	time.Sleep(1500 * time.Millisecond)

	// 确认会话已被清理
	sessLock.Lock()
	_, ok := sessions[sessionID]
	sessLock.Unlock()
	if ok {
		t.Error("session should have been released after timeout")
	}
}

// TestSessionReleaseTimerMultiSession 多个会话独立释放
func TestSessionReleaseTimerMultiSession(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
	}()

	obj1 := &sessionObj{
		cwd:  "/tmp/test_multi_1",
		id:   99993,
		loop: loop.New(nil),
	}
	obj2 := &sessionObj{
		cwd:  "/tmp/test_multi_2",
		id:   99994,
		loop: loop.New(nil),
	}
	sid1 := cwd2SessionID(obj1.cwd, obj1.id)
	sid2 := cwd2SessionID(obj2.cwd, obj2.id)
	sessions[sid1] = obj1
	sessions[sid2] = obj2
	agentCallList[sid1] = make(map[string]func())
	agentCallList[sid2] = make(map[string]func())

	// 只调度释放 session 1
	scheduleSessionRelease(sid1)

	// 确认 session 2 仍在
	if _, ok := sessions[sid2]; !ok {
		t.Error("session 2 should still exist")
	}

	// 取消 session 1
	cancelSessionRelease(sid1)

	if _, ok := sessions[sid1]; !ok {
		t.Error("session 1 should still exist after cancel")
	}
}

// TestRegisterConnCallCancelsReleaseTimer registerConnCall 应取消待处理的定时器
func TestRegisterConnCallCancelsReleaseTimer(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	oldSessionTimeout := config.GlobalConfig.Server.SessionTimeout
	oldSessionConnMap := sessionConnMap
	oldConnCallMap := connCallMap
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	sessionConnMap = map[string][]uint64{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	config.GlobalConfig.Server.SessionTimeout = 3 // 3 秒超时，给足够时间让 register 取消
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
		sessionConnMap = oldSessionConnMap
		connCallMap = oldConnCallMap
		config.GlobalConfig.Server.SessionTimeout = oldSessionTimeout
	}()

	obj := &sessionObj{
		cwd:  "/tmp/test_register_cancel",
		id:   99995,
		loop: loop.New(nil),
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 先调度释放
	scheduleSessionRelease(sessionID)

	// 模拟新连接注册 — 应取消定时器
	registerConnCall(12345, sessionID, func(_ string, _ any, _ *string) error { return nil })

	// 等待足够长（确认定时器被取消，不会触发释放）
	time.Sleep(500 * time.Millisecond)

	sessLock.Lock()
	_, ok := sessions[sessionID]
	sessLock.Unlock()
	if !ok {
		t.Error("session should still exist after registerConnCall cancels the release timer")
	}
	if obj.releaseTimer != nil {
		t.Error("releaseTimer should be nil after registerConnCall")
	}
}

// TestSessionDelete_ColdSession 冷会话（未 new/resume）按字符串解析直接删除
func TestSessionDelete_ColdSession(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 1)
	id := ids[0]

	// 插入一条消息，验证子表级联删除
	msg := structs.Messages{ChatID: id}
	if err := db.Create(&msg).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	// 不经过 SessionNew/SessionResume，直接以字符串形式构造 sessionId 删除
	sessionID := cwd2SessionID(dir, id)
	if _, err := SessionDelete(SessionDeleteRequest{SessionID: sessionID}, nil, 1); err != nil {
		t.Fatalf("SessionDelete failed: %v", err)
	}

	// 会话已从 DB 删除
	if _, err := funcs.QueryChat(db, id); err == nil {
		t.Error("chat should be deleted from DB")
	}
	// 子记录已级联删除
	var cnt int64
	if err := db.Model(&structs.Messages{}).Where("chat_id = ?", id).Count(&cnt).Error; err != nil {
		t.Fatalf("count messages failed: %v", err)
	}
	if cnt != 0 {
		t.Errorf("messages should be cascade-deleted, got %d", cnt)
	}
	// session/list 不再返回该会话
	resp, err := SessionList(SessionListRequest{Cwd: dir}, nil, 1)
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	for _, s := range resp.Sessions {
		if s.SessionID == sessionID {
			t.Error("deleted session should not appear in session/list")
		}
	}
}

// TestSessionDelete_NonexistentCwd cwd 路径不存在时静默成功
func TestSessionDelete_NonexistentCwd(t *testing.T) {
	if _, err := SessionDelete(SessionDeleteRequest{
		SessionID: "sess_1:/nonexistent/path/alkaid0_xyz",
	}, nil, 1); err != nil {
		t.Errorf("expected silent success, got %v", err)
	}
}

// TestSessionDelete_InvalidID 畸形 sessionId 静默成功
func TestSessionDelete_InvalidID(t *testing.T) {
	if _, err := SessionDelete(SessionDeleteRequest{SessionID: "not-a-session-id"}, nil, 1); err != nil {
		t.Errorf("expected silent success, got %v", err)
	}
}

// TestSessionDelete_LoadedSession 已加载会话删除：内存注册表移除 + DB 记录删除
func TestSessionDelete_LoadedSession(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
	}()

	dir, db, ids := newSessionListDB(t, 1)
	id := ids[0]

	// 构造已加载会话对象并注册到内存注册表（DB 引用留空，避免 closeSession 触发后台索引）
	sessionID := cwd2SessionID(dir, id)
	obj := &sessionObj{
		cwd:     dir,
		id:      id,
		session: &structs.Chats{ID: id, ReferCount: 1},
		loop:    loop.New(nil),
		ctx:     context.Background(),
	}
	sessLock.Lock()
	sessions[sessionID] = obj
	sessLock.Unlock()

	if _, err := SessionDelete(SessionDeleteRequest{SessionID: sessionID}, nil, 1); err != nil {
		t.Fatalf("SessionDelete failed: %v", err)
	}

	// 内存注册表已移除
	sessLock.Lock()
	_, ok := sessions[sessionID]
	sessLock.Unlock()
	if ok {
		t.Error("session should be removed from in-memory registry")
	}
	// DB 记录已删除
	if _, err := funcs.QueryChat(db, id); err == nil {
		t.Error("chat should be deleted from DB")
	}
}

// SessionDelete 测试时需要真实 db path
// TestSessionDeleteCancelsTimer SessionDelete 应取消定时器
func TestSessionDeleteCancelsTimer(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
	}()

	// SessionDelete 会调用 closeSession -> loop.Cancel -> closeDB
	// 这里只验证 cancelSessionRelease 被调用（在 closeSession 之前）
	obj := &sessionObj{
		cwd:  "/tmp/test_delete_cancel",
		id:   99996,
		loop: loop.New(nil),
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 调度释放
	scheduleSessionRelease(sessionID)

	// SessionDelete 内部逻辑：取消定时器 + closeSession
	cancelSessionRelease(sessionID)

	if obj.releaseTimer != nil {
		t.Error("releaseTimer should be nil after cancel")
	}
}

// --- 后台模式（background）测试 ---

// TestBackgroundDefaultFalse 新建 session 的 background 应为 false
func TestBackgroundDefaultFalse(t *testing.T) {
	obj := &sessionObj{
		// cwd:  "/tmp",
		// id:   88880,
		// loop: loop.New(nil),
	}
	if obj.background {
		t.Error("new sessionObj should have background=false by default")
	}
}

// TestGetBackgroundSessionNotFound 无效 sessionID 应返回错误
func TestGetBackgroundSessionNotFound(t *testing.T) {
	oldSessions := sessions
	sessions = map[string]*sessionObj{}
	defer func() { sessLock.Lock(); sessions = oldSessions; sessLock.Unlock() }()

	_, err := SessionGetBackground(SessionGetBackgroundRequest{
		SessionID: "sess_99999:/nonexistent",
	}, nil, 1)
	if err == nil {
		t.Error("expected error for non-existent session")
	}
}

// TestGetBackgroundSession 创建 session 后查询 background 状态
func TestGetBackgroundSession(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
	}()

	obj := &sessionObj{
		cwd:  "/tmp/test_get_bg",
		id:   88881,
		loop: loop.New(nil),
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 默认状态应为 false
	resp, err := SessionGetBackground(SessionGetBackgroundRequest{
		SessionID: sessionID,
	}, nil, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Background {
		t.Error("default background should be false")
	}

	// 设置 background=true 后查询
	obj.background = true
	resp, err = SessionGetBackground(SessionGetBackgroundRequest{
		SessionID: sessionID,
	}, nil, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Background {
		t.Error("background should be true after setting")
	}
}

// TestScheduleReleaseBackgroundActive background=true + 活跃状态 → 不释放，重新调度
func TestScheduleReleaseBackgroundActive(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	oldSessionTimeout := config.GlobalConfig.Server.SessionTimeout
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	config.GlobalConfig.Server.SessionTimeout = 1 // 1 秒超时
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
		config.GlobalConfig.Server.SessionTimeout = oldSessionTimeout
	}()

	obj := &sessionObj{
		cwd:  "/tmp/test_bg_active",
		id:   88882,
		loop: loop.New(nil),
		session: &structs.Chats{
			State: state.StateRequesting, // 活跃处理中
		},
		background: true,
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 调度释放
	scheduleSessionRelease(sessionID)

	// 等待超时
	time.Sleep(1500 * time.Millisecond)

	// session 应仍然存在（被重新调度了）
	sessLock.Lock()
	_, ok := sessions[sessionID]
	sessLock.Unlock()
	if !ok {
		t.Error("session should still exist when background mode is on and state is active")
	}
	// releaseTimer 应被重新设置（非 nil）
	if obj.releaseTimer == nil {
		t.Error("releaseTimer should be rescheduled when background mode is on and state is active")
	}
}

// TestScheduleReleaseBackgroundIdle background=true + StateIdle → 释放
func TestScheduleReleaseBackgroundIdle(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	oldSessionTimeout := config.GlobalConfig.Server.SessionTimeout
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	config.GlobalConfig.Server.SessionTimeout = 1 // 1 秒超时
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
		config.GlobalConfig.Server.SessionTimeout = oldSessionTimeout
	}()

	obj := &sessionObj{
		cwd:  "/tmp/test_bg_idle",
		id:   88883,
		loop: loop.New(nil),
		session: &structs.Chats{
			State: state.StateIdle, // 空闲
		},
		background: true,
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 调度释放
	scheduleSessionRelease(sessionID)

	// 等待超时
	time.Sleep(1500 * time.Millisecond)

	// session 应被释放（Idle 状态下即使 background=true 也应释放）
	sessLock.Lock()
	_, ok := sessions[sessionID]
	sessLock.Unlock()
	if ok {
		t.Error("session should be released when background mode is on but state is idle")
	}
}

// TestScheduleReleaseBackgroundWaitApprove background=true + StateWaitApprove → 释放
func TestScheduleReleaseBackgroundWaitApprove(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	oldSessionTimeout := config.GlobalConfig.Server.SessionTimeout
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	config.GlobalConfig.Server.SessionTimeout = 1 // 1 秒超时
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
		config.GlobalConfig.Server.SessionTimeout = oldSessionTimeout
	}()

	obj := &sessionObj{
		cwd:  "/tmp/test_bg_waitapp",
		id:   88884,
		loop: loop.New(nil),
		session: &structs.Chats{
			State: state.StateWaitApprove, // 等待审批
		},
		background: true,
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 调度释放
	scheduleSessionRelease(sessionID)

	// 等待超时
	time.Sleep(1500 * time.Millisecond)

	// session 应被释放（WaitApprove 状态下即使 background=true 也应释放）
	sessLock.Lock()
	_, ok := sessions[sessionID]
	sessLock.Unlock()
	if ok {
		t.Error("session should be released when background mode is on but state is WaitApprove")
	}
}

// TestScheduleReleaseBackgroundOff background=false → 始终释放（回归测试）
func TestScheduleReleaseBackgroundOff(t *testing.T) {
	oldSessions := sessions
	oldAgentCallList := agentCallList
	oldSessionTimeout := config.GlobalConfig.Server.SessionTimeout
	sessions = map[string]*sessionObj{}
	agentCallList = map[string]map[string]func(){}
	config.GlobalConfig.Server.SessionTimeout = 1 // 1 秒超时
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		agentCallList = oldAgentCallList
		config.GlobalConfig.Server.SessionTimeout = oldSessionTimeout
	}()

	obj := &sessionObj{
		cwd:  "/tmp/test_bg_off",
		id:   88885,
		loop: loop.New(nil),
		session: &structs.Chats{
			State: state.StateRequesting, // 即使活跃
		},
		background: false, // background=off
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessions[sessionID] = obj
	agentCallList[sessionID] = make(map[string]func())

	// 调度释放
	scheduleSessionRelease(sessionID)

	// 等待超时
	time.Sleep(1500 * time.Millisecond)

	// session 应被释放（background=false 时即使活跃也应释放）
	sessLock.Lock()
	_, ok := sessions[sessionID]
	sessLock.Unlock()
	if ok {
		t.Error("session should be released when background mode is off, even if state is active")
	}
}

// terminalHistoryTestJob 提交一次前台命令并等待结束，返回任务。
func terminalHistoryTestJob(t *testing.T, chatID uint32, cwd, command string) *runTool.Job {
	t.Helper()
	job, err := runTool.Default.Submit(context.Background(), &runTool.Request{
		SessionID: chatID,
		Command:   command,
		Shell:     "sh",
		WorkDir:   cwd,
		Sandbox:   false,
	})
	if err != nil {
		t.Fatalf("submit %q failed: %v", command, err)
	}
	job.Wait(context.Background())
	return job
}

// replayTestDelta 构造 MessagesRoleTool.Delta 格式的工具结果（[{"name","id","return"}]）。
func replayTestDelta(t *testing.T, entries ...map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal tool results failed: %v", err)
	}
	return string(raw)
}

// replayTestResult 构造单个工具结果 JSON 字符串（return 字段的内容）。
func replayTestResult(t *testing.T, res map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal tool result failed: %v", err)
	}
	return string(raw)
}

// TestReplayRunTerminalIDs 验证从持久化的工具结果反推 run 调用的终端 ID。
func TestReplayRunTerminalIDs(t *testing.T) {
	delta := replayTestDelta(t,
		// 后台 run：run_id 与 path 均为 @temp/run/<n>
		map[string]any{"name": "run", "id": "tool_bg", "return": replayTestResult(t, map[string]any{"success": true, "background": true, "path": "@temp/run/3", "run_id": "@temp/run/3"})},
		// 前台 run：只有 path
		map[string]any{"name": "run", "id": "tool_fg", "return": replayTestResult(t, map[string]any{"success": true, "path": "@temp/run/7", "output": "hi"})},
		// 降级路径：path 是输出文本，不构成终端
		map[string]any{"name": "run", "id": "tool_fallback", "return": replayTestResult(t, map[string]any{"success": true, "path": "command output text"})},
		// 非 run 工具即便 path 形如 run/1 也不推导
		map[string]any{"name": "edit", "id": "tool_edit", "return": replayTestResult(t, map[string]any{"path": "run/1"})},
	)

	ids := replayRunTerminalIDs(delta)
	if ids["tool_bg"] != "@temp/run/3" {
		t.Errorf("tool_bg terminal id = %q, want @temp/run/3", ids["tool_bg"])
	}
	if ids["tool_fg"] != "@temp/run/7" {
		t.Errorf("tool_fg terminal id = %q, want @temp/run/7", ids["tool_fg"])
	}
	if _, ok := ids["tool_fallback"]; ok {
		t.Error("降级路径（path 为输出文本）不应产生 terminal id")
	}
	if _, ok := ids["tool_edit"]; ok {
		t.Error("非 run 工具不应产生 terminal id")
	}
	if len(replayRunTerminalIDs("")) != 0 || len(replayRunTerminalIDs("not json")) != 0 {
		t.Error("空/非法结果应返回空映射")
	}
}

// TestSessionResumeReplayTerminalID 验证历史回放的 run 工具调用同样携带 terminal id：
// 服务端重启后客户端凭回放的工具调用即可查询终端内容。
func TestSessionResumeReplayTerminalID(t *testing.T) {
	oldSessions := sessions
	oldDbs := dbs
	sessions = map[string]*sessionObj{}
	dbs = map[string]*dbObj{}
	defer func() {
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		dbLock.Lock()
		dbs = oldDbs
		dbLock.Unlock()
	}()
	if config.GlobalConfig == nil {
		config.GlobalConfigSwap(cfgStructs.Config{})
	}

	tmpDir := t.TempDir()
	t.Cleanup(func() { _ = codebase.CloseDirectory(tmpDir) })
	db, err := storage.InitStorage(path.Join(tmpDir, ".alkaid0"), "")
	if err != nil {
		t.Fatalf("InitStorage failed: %v", err)
	}
	defer u.Unwrap(db.DB()).Close()
	chatID, err := funcs.CreateChat(db)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	sessionID := cwd2SessionID(tmpDir, chatID)

	// 落库一条 run 工具调用与它的结果（格式与生产一致）
	calls, err := json.Marshal([]map[string]any{{
		"name":       "run",
		"id":         "tool_1",
		"parameters": map[string]any{"type": "shell", "reason": "t", "command": "echo hi"},
	}})
	if err != nil {
		t.Fatalf("marshal tool calling failed: %v", err)
	}
	if err := db.Create(&structs.Messages{
		ChatID:                chatID,
		Type:                  structs.MessagesRoleAgent,
		Delta:                 "running command",
		ToolCallingJSONString: string(calls),
	}).Error; err != nil {
		t.Fatalf("insert agent message failed: %v", err)
	}
	if err := db.Create(&structs.Messages{
		ChatID: chatID,
		Type:   structs.MessagesRoleTool,
		Delta: replayTestDelta(t, map[string]any{
			"name":   "run",
			"id":     "tool_1",
			"return": replayTestResult(t, map[string]any{"success": true, "path": "@temp/run/9"}),
		}),
	}).Error; err != nil {
		t.Fatalf("insert tool message failed: %v", err)
	}

	var replayed []SessionUpdateUpdate
	call := func(method string, params any, _ *string) error {
		if method != "session/update" {
			return nil
		}
		if update, ok := params.(SessionUpdate); ok {
			if val, ok := update.Update.(SessionUpdateUpdate); ok {
				replayed = append(replayed, val)
			}
		}
		return nil
	}
	if _, err := SessionResume(SessionResumeRequest{
		Cwd:        tmpDir,
		SessionID:  sessionID,
		ReplayFrom: &ReplayFrom{Type: "start"},
	}, call, 1); err != nil {
		t.Fatalf("SessionResume replay failed: %v", err)
	}

	found := false
	for _, update := range replayed {
		if update.SessionUpdate != "tool_call_update" || update.ToolCallID == "" {
			continue
		}
		if update.Kind == "execute" {
			found = true
			if update.ToolTerminalID != "@temp/run/9" {
				t.Errorf("回放的 run 工具调用应携带 terminal id @temp/run/9，实际 %q", update.ToolTerminalID)
			}
		}
	}
	if !found {
		t.Fatalf("回放应包含 run 工具调用（execute），实际 %+v", replayed)
	}

	// 等待异步索引并释放会话（避免 TempDir 清理时文件被占用）
	sessLock.Lock()
	obj := sessions[sessionID]
	sessLock.Unlock()
	if obj != nil && obj.indexDone != nil {
		select {
		case <-obj.indexDone:
		case <-time.After(10 * time.Second):
			t.Error("timeout waiting for async index goroutine")
		}
	}
	closeSession(sessionID)
}

// TestToolCallUpdateCarriesTerminalID 验证 tool_call_update 顶层携带
// alk.cxykevin.top/terminal_id（终端内容查询用），且不占用标准 terminalId 字段。
func TestToolCallUpdateCarriesTerminalID(t *testing.T) {
	raw, err := json.Marshal(SessionUpdateUpdate{
		SessionUpdate:  "tool_call_update",
		ToolCallID:     "call_1",
		RunID:          "@temp/run/7",
		ToolTerminalID: "@temp/run/7",
	})
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded["alk.cxykevin.top/terminal_id"] != "@temp/run/7" {
		t.Errorf("expected alk.cxykevin.top/terminal_id=@temp/run/7, got %v", decoded)
	}
	if decoded["alk.cxykevin.top/run_id"] != "@temp/run/7" {
		t.Errorf("expected alk.cxykevin.top/run_id=@temp/run/7, got %v", decoded)
	}
	if _, ok := decoded["terminalId"]; ok {
		t.Errorf("tool_call_update 不应占用标准 terminalId 字段: %v", decoded)
	}
}

// TestSessionTerminalHistory 验证已结束终端内容查询：
// 只返回已结束终端、携带最终完整内容，并通过 callback 复用终端全量推送。
func TestSessionTerminalHistory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	const chatID uint32 = 471100
	cwd := t.TempDir()
	sessionID := registerTestSession(t, cwd, chatID)

	// 已结束的前台终端
	ended := terminalHistoryTestJob(t, chatID, cwd, "echo history-ended")
	if ended.Status() == runTool.JobRunning {
		t.Fatalf("expected job %s to be finished", ended.ID)
	}

	// 运行中的终端：不进入已结束列表
	runningReq := &runTool.Request{SessionID: chatID, Command: "sleep 10", Shell: "sh", WorkDir: cwd}
	running, err := runTool.Default.Submit(context.Background(), runningReq)
	if err != nil {
		t.Fatalf("submit running job failed: %v", err)
	}
	t.Cleanup(func() {
		_ = runTool.Default.Kill(cwd, running.ID)
		<-running.Done()
	})

	// 其它会话的已结束终端：不应混入
	const otherChatID uint32 = 471101
	otherSessionID := registerTestSession(t, cwd, otherChatID)
	other := terminalHistoryTestJob(t, otherChatID, cwd, "echo history-other")

	var pushed []SessionUpdateUpdate
	call := func(method string, params any, _ *string) error {
		if method != "session/update" {
			return nil
		}
		update, ok := params.(SessionUpdate)
		if !ok {
			t.Errorf("unexpected session/update payload type %T", params)
			return nil
		}
		if u, ok := update.Update.(SessionUpdateUpdate); ok {
			pushed = append(pushed, u)
		}
		return nil
	}

	// 1) 按会话列出全部已结束终端
	resp, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID}, call, 1)
	if err != nil {
		t.Fatalf("SessionTerminalHistory failed: %v", err)
	}
	ids := make(map[string]SessionTerminalInfo, len(resp.Terminals))
	for _, item := range resp.Terminals {
		ids[item.TerminalID] = item
	}
	got, ok := ids[ended.ID]
	if !ok {
		t.Fatalf("expected ended terminal %s in history, got %+v", ended.ID, resp.Terminals)
	}
	if !strings.Contains(got.Content, "history-ended") {
		t.Errorf("expected history content to include command output, got %q", got.Content)
	}
	if got.Status != "finished" {
		t.Errorf("expected status finished, got %q", got.Status)
	}
	if got.Kind != "foreground" {
		t.Errorf("expected kind foreground, got %q", got.Kind)
	}
	if _, ok := ids[running.ID]; ok {
		t.Error("history should not contain a running terminal")
	}
	if _, ok := ids[other.ID]; ok {
		t.Error("history should not contain terminals of another session")
	}

	// 复用终端全量推送：一条 updateType=full 的 terminal_update
	if len(pushed) != 1 {
		t.Fatalf("expected exactly one pushed terminal update, got %d", len(pushed))
	}
	if pushed[0].SessionUpdate != "alk.cxykevin.top/terminal_update" || pushed[0].TerminalUpdateType != "full" {
		t.Errorf("unexpected pushed update: %+v", pushed[0])
	}
	if len(pushed[0].Terminals) != len(resp.Terminals) {
		t.Errorf("pushed terminals = %d, want %d", len(pushed[0].Terminals), len(resp.Terminals))
	}

	// 2) 按 terminalId 取单个已结束终端：顶层带 terminalId/status/content
	pushed = nil
	single, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID, TerminalID: ended.ID}, call, 1)
	if err != nil {
		t.Fatalf("SessionTerminalHistory(single) failed: %v", err)
	}
	if len(single.Terminals) != 1 || single.Terminals[0].TerminalID != ended.ID {
		t.Fatalf("expected only terminal %s, got %+v", ended.ID, single.Terminals)
	}
	if !strings.Contains(single.Terminals[0].Content, "history-ended") {
		t.Errorf("expected single history content, got %q", single.Terminals[0].Content)
	}
	if len(pushed) != 1 {
		t.Fatalf("expected one pushed update for single query, got %d", len(pushed))
	}
	if pushed[0].TerminalID != ended.ID || pushed[0].Status != "stop" {
		t.Errorf("unexpected single push header: %+v", pushed[0])
	}
	if pushed[0].Content != single.Terminals[0].Content {
		t.Error("pushed content should equal the queried terminal content")
	}

	// 3) 错误分支：前缀不合法的 ID / 未知终端 / 其它会话终端 / 仍在运行
	if _, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID, TerminalID: "run_1"}, call, 1); err == nil {
		t.Error("expected error for terminalId without @temp/run/ prefix")
	}
	if _, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID, TerminalID: "@temp/run/does-not-exist"}, call, 1); err == nil {
		t.Error("expected error for unknown terminalId")
	}
	if _, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: otherSessionID, TerminalID: ended.ID}, call, 1); err == nil {
		t.Error("expected error for terminal of another session")
	}
	if _, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID, TerminalID: running.ID}, call, 1); err == nil {
		t.Error("expected error for still running terminal")
	}
	if _, err := SessionTerminalHistory(SessionTerminalHistoryRequest{}, call, 1); err == nil {
		t.Error("expected error for empty sessionId")
	}
}

// TestSessionTerminalHistoryRestored 验证服务端重启后（内存中已无该终端）仍能按
// terminal id 从持久化副本（ReferFiles 中的 @temp/run/<n>）取回终端内容。
func TestSessionTerminalHistoryRestored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows")
	}

	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".alkaid0"), 0o755); err != nil {
		t.Fatalf("create .alkaid0: %v", err)
	}
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB failed: %v", err)
	}
	defer closeDB(cwd)
	chatID, err := funcs.CreateChat(db, false)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	sessionID := registerTestSession(t, cwd, chatID)

	// 模拟"重启前的终端"：内存中没有对应 job，只有持久化副本。
	// 新分配的 ID 不会被任何存活任务占用；temp obj 内部路径为 run/<n>。
	terminalID := runTool.NewRunID(cwd)
	runPath, ok := runTool.TempPath(terminalID)
	if !ok {
		t.Fatalf("invalid run id: %s", terminalID)
	}
	const stored = "restored terminal output"
	if err := db.Create(&structs.ReferFiles{ChatID: chatID, Path: runPath, Content: stored, ReadOnly: true}).Error; err != nil {
		t.Fatalf("insert refer file failed: %v", err)
	}

	var pushed []SessionUpdateUpdate
	call := func(method string, params any, _ *string) error {
		if method != "session/update" {
			return nil
		}
		if update, ok := params.(SessionUpdate); ok {
			if u, ok := update.Update.(SessionUpdateUpdate); ok {
				pushed = append(pushed, u)
			}
		}
		return nil
	}

	// 1) 按 terminal id 取回持久化内容
	resp, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID, TerminalID: terminalID}, call, 1)
	if err != nil {
		t.Fatalf("SessionTerminalHistory failed: %v", err)
	}
	if len(resp.Terminals) != 1 {
		t.Fatalf("expected 1 terminal, got %+v", resp.Terminals)
	}
	got := resp.Terminals[0]
	if got.Content != stored {
		t.Errorf("content = %q, want %q", got.Content, stored)
	}
	if !got.Restored {
		t.Error("持久化恢复的条目应标记 restored")
	}
	if got.TerminalID != terminalID || got.SessionID != sessionID {
		t.Errorf("unexpected terminal identity: %+v", got)
	}
	if len(pushed) != 1 || pushed[0].TerminalID != terminalID || pushed[0].Status != "stop" || pushed[0].Content != stored {
		t.Errorf("unexpected pushed update: %+v", pushed)
	}

	// 2) 列表查询同样包含该终端（无需 terminalId）
	list, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID}, nil, 1)
	if err != nil {
		t.Fatalf("SessionTerminalHistory(list) failed: %v", err)
	}
	found := false
	for _, item := range list.Terminals {
		if item.TerminalID == terminalID {
			found = true
			if item.Content != stored {
				t.Errorf("list content = %q, want %q", item.Content, stored)
			}
		}
	}
	if !found {
		t.Errorf("list 应包含持久化恢复的终端 %s，实际 %+v", terminalID, list.Terminals)
	}

	// 3) 无持久化副本的终端仍报 not found
	if _, err := SessionTerminalHistory(SessionTerminalHistoryRequest{SessionID: sessionID, TerminalID: "run_does_not_exist"}, call, 1); err == nil {
		t.Error("expected error for unknown terminalId")
	}
}

// 辅助函数
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && strings.Contains(s, substr)
}
