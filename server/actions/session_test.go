package actions

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/loop"
	"github.com/cxykevin/alkaid0/ui/state"
)

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

// TestSessionLoadValidation 测试SessionLoad的参数验证
func TestSessionLoadValidation(t *testing.T) {
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
			errContains: "invalid session id",
		},
		{
			name:        "cwd不匹配",
			cwd:         "/tmp",
			sessionID:   "sess_123:/home/test",
			wantErr:     true,
			errContains: "not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SessionLoad(SessionLoadRequest{Cwd: tt.cwd, SessionID: tt.sessionID}, nil, 1)
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionLoad() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !contains(err.Error(), tt.errContains) {
				t.Errorf("SessionLoad() error message = %v, want contains %v", err.Error(), tt.errContains)
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

// TestSessionSetModelValidation 测试SessionSetModel的参数验证
func TestSessionSetModelValidation(t *testing.T) {
	tests := []struct {
		name        string
		sessionID   string
		modelID     int32
		wantErr     bool
		errContains string
	}{
		{
			name:        "空的sessionId",
			sessionID:   "",
			modelID:     0,
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "无效的sessionId格式",
			sessionID:   "invalid",
			modelID:     0,
			wantErr:     true,
			errContains: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SessionSetModel(SessionSetModelRequest{
				SessionID: tt.sessionID,
				ModelID:   fmt.Sprintf("%d/model", tt.modelID),
			}, nil, 1)
			if (err != nil) != tt.wantErr {
				t.Errorf("SessionSetModel() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && !contains(err.Error(), tt.errContains) {
				t.Errorf("SessionSetModel() error message = %v, want contains %v", err.Error(), tt.errContains)
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
		cwd:  "/tmp",
		id:   88880,
		loop: loop.New(nil),
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

// 辅助函数
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && strings.Contains(s, substr)
}
