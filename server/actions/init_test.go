package actions

import (
	"testing"

	u "github.com/cxykevin/alkaid0/utils"
)

func v2InitReq() InitializeRequest {
	return InitializeRequest{
		ProtocolVersion: 2,
		Capabilities:    u.H{},
		Info:            u.H{"name": "test-client", "title": "Test", "version": "1"},
	}
}

// TestInitializeResponse 测试Initialize方法的响应格式（ACP v2）
func TestInitializeResponse(t *testing.T) {
	req := v2InitReq()

	resp, err := Initialize(req, nil, 1)
	if err != nil {
		t.Fatalf("Initialize返回错误: %v", err)
	}

	if resp.ProtocolVersion != 2 {
		t.Errorf("协议版本应为 2，got %d", resp.ProtocolVersion)
	}
	if resp.Info == nil || resp.Info["name"] != "alkaid0" {
		t.Errorf("info.name 应为 alkaid0，got %v", resp.Info)
	}
	if resp.Capabilities == nil {
		t.Fatal("capabilities 不应为 nil")
	}
	session, ok := resp.Capabilities["session"].(u.H)
	if !ok {
		t.Fatal("capabilities.session 应为对象")
	}
	if _, ok := session["prompt"]; !ok {
		t.Error("capabilities.session.prompt 应存在")
	}
	if len(resp.AuthMethods) != 0 {
		t.Error("authMethods 应为空（不广告 auth 表面）")
	}
}

// TestInitializeVersionMismatch 协议版本不匹配应报错
func TestInitializeVersionMismatch(t *testing.T) {
	req := v2InitReq()
	req.ProtocolVersion = 1
	if _, err := Initialize(req, nil, 1); err == nil {
		t.Error("protocolVersion=1 应被拒绝")
	}
}

// TestProtocolVersion 测试协议版本号
func TestProtocolVersion(t *testing.T) {
	resp, err := Initialize(v2InitReq(), nil, 1)
	if err != nil {
		t.Fatalf("Initialize返回错误: %v", err)
	}
	if resp.ProtocolVersion != protoVersion {
		t.Errorf("协议版本应为 %d", protoVersion)
	}
}

// TestServerCapabilities 测试服务器能力声明（ACP v2：标记为 {} 对象而非布尔）
func TestServerCapabilities(t *testing.T) {
	resp, err := Initialize(v2InitReq(), nil, 1)
	if err != nil {
		t.Fatalf("Initialize返回错误: %v", err)
	}
	if len(resp.Capabilities) == 0 {
		t.Error("capabilities 不应为空")
	}
	// v2 能力标记必须是对象（非布尔）
	session := resp.Capabilities["session"].(u.H)
	prompt := session["prompt"].(u.H)
	if _, ok := prompt["image"].(u.H); !ok {
		t.Error("session.prompt.image 应为 {} 对象")
	}
	// delete 为可选扩展能力，已实现 session/delete，必须声明
	if _, ok := session["delete"].(u.H); !ok {
		t.Error("capabilities.session.delete 应存在（服务端已实现 session/delete）")
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
			name:    "有效的初始化请求",
			request: v2InitReq(),
			wantErr: false,
		},
		{
			name: "协议版本不匹配",
			request: InitializeRequest{
				ProtocolVersion: 1,
				Capabilities:    u.H{},
				Info:            u.H{"name": "test"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Initialize(tt.request, nil, 1)
			if (err != nil) != tt.wantErr {
				t.Errorf("Initialize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
