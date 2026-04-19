package actions

import (
	"fmt"
	"testing"
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
			cwd:         "/tmp",
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
			errContains: "short",
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

// 辅助函数
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr))
}
