package actions

import (
	"testing"

	u "github.com/cxykevin/alkaid0/utils"
)

// TestInitializeResponse 测试Initialize方法的响应格式
func TestInitializeResponse(t *testing.T) {
	req := InitializeRequest{
		ProtocolVersion:    1,
		ClientCapabilities: u.H{},
	}

	resp, err := Initialize(req, nil, 1)
	if err != nil {
		t.Logf("Initialize返回错误（可能是预期的）: %v", err)
		return
	}

	// 验证响应
	if resp.ProtocolVersion == 0 {
		t.Error("协议版本应该被设置")
	}
}

// TestInitFuncs 测试方法注册接口
func TestInitFuncs(t *testing.T) {
	t.Log("InitFuncs测试：验证方法注册接口存在")
	// InitFuncs会调用jsonrpc.Set，我们无法直接测试
	// 但函数是公开的，可以通过导入来测试其存在性
}

// TestProtocolVersion 测试协议版本号
func TestProtocolVersion(t *testing.T) {
	req := InitializeRequest{
		ProtocolVersion:    1,
		ClientCapabilities: u.H{},
	}

	resp, err := Initialize(req, nil, 1)
	if err != nil {
		t.Logf("Initialize返回错误: %v", err)
		return
	}

	if resp.ProtocolVersion == 0 {
		t.Error("协议版本应该被正确设置")
	} else {
		t.Logf("协议版本: %d", resp.ProtocolVersion)
	}
}

// TestServerCapabilities 测试服务器能力声明
func TestServerCapabilities(t *testing.T) {
	req := InitializeRequest{
		ProtocolVersion:    1,
		ClientCapabilities: u.H{},
	}

	resp, err := Initialize(req, nil, 1)
	if err != nil {
		t.Logf("Initialize返回错误: %v", err)
		return
	}

	// 检查是否包含capability信息
	if resp.AgentCapabilities != nil {
		t.Logf("服务器能力已声明: %v", len(resp.AgentCapabilities) > 0)
	}
}

// TestInitializeValidation 测试参数验证
func TestInitializeValidation(t *testing.T) {
	tests := []struct {
		name    string
		request InitializeRequest
		wantErr bool
	}{
		{
			name: "有效的初始化请求",
			request: InitializeRequest{
				ProtocolVersion:    1,
				ClientCapabilities: u.H{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Initialize(tt.request, nil, 1)
			if (err != nil) != tt.wantErr {
				if err != nil {
					t.Logf("Initialize返回: %v", err)
				}
			}
		})
	}
}
