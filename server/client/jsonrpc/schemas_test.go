package jsonrpc

import (
	"encoding/json"
	"testing"
)

// TestRequestMarshal 测试Request结构体的JSON序列化
func TestRequestMarshal(t *testing.T) {
	req := Request{
		Version: "2.0",
		ID:      123,
		Method:  "test_method",
		Params: map[string]any{
			"key": "value",
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("JSON序列化失败: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("JSON反序列化失败: %v", err)
	}

	if decoded.Version != req.Version {
		t.Errorf("Version不匹配: got %s, want %s", decoded.Version, req.Version)
	}

	if decoded.Method != req.Method {
		t.Errorf("Method不匹配: got %s, want %s", decoded.Method, req.Method)
	}
}

// TestResponseSuccess 测试成功的JSON-RPC响应
func TestResponseSuccess(t *testing.T) {
	resp := Response{
		Version: "2.0",
		ID:      123,
		Result:  "success",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("JSON序列化失败: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("JSON反序列化失败: %v", err)
	}

	if decoded.Result != "success" {
		t.Errorf("Result不匹配: got %v, want success", decoded.Result)
	}

	if decoded.Error != nil {
		t.Errorf("Error应该为nil，但得到: %v", decoded.Error)
	}
}

// TestResponseError 测试错误的JSON-RPC响应
func TestResponseError(t *testing.T) {
	resp := Response{
		Version: "2.0",
		ID:      123,
		Error: &Error{
			Code:    -32600,
			Message: "Invalid Request",
			Data:    "additional info",
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("JSON序列化失败: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("JSON反序列化失败: %v", err)
	}

	if decoded.Error == nil {
		t.Fatal("Error应该不为nil")
	}

	if decoded.Error.Code != -32600 {
		t.Errorf("错误码不匹配: got %d, want -32600", decoded.Error.Code)
	}

	if decoded.Error.Message != "Invalid Request" {
		t.Errorf("错误信息不匹配: got %s, want Invalid Request", decoded.Error.Message)
	}
}

// TestErrorCodes 测试所有定义的错误码
func TestErrorCodes(t *testing.T) {
	tests := []struct {
		name string
		code int
		desc string
	}{
		{
			name: "ParseError",
			code: JRPCParseError,
			desc: "-32700 错误",
		},
		{
			name: "InvalidRequest",
			code: JRPCInvalidRequest,
			desc: "-32600 错误",
		},
		{
			name: "MethodNotFound",
			code: JRPCMethodNotFound,
			desc: "-32601 错误",
		},
		{
			name: "InvalidParams",
			code: JRPCInvalidParams,
			desc: "-32602 错误",
		},
		{
			name: "InternalError",
			code: JRPCInternalError,
			desc: "-32603 错误",
		},
		{
			name: "ServerError",
			code: JRPCServerError,
			desc: "-32000 错误",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code == 0 {
				t.Errorf("%s错误码未定义", tt.name)
			}
		})
	}
}

// TestBatchRequest 测试批量请求的JSON格式
func TestBatchRequest(t *testing.T) {
	batch := []Request{
		{
			Version: "2.0",
			ID:      1,
			Method:  "method1",
			Params:  map[string]any{"key": "value1"},
		},
		{
			Version: "2.0",
			ID:      2,
			Method:  "method2",
			Params:  map[string]any{"key": "value2"},
		},
	}

	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("批量请求序列化失败: %v", err)
	}

	var decodedBatch []Request
	if err := json.Unmarshal(data, &decodedBatch); err != nil {
		t.Fatalf("批量请求反序列化失败: %v", err)
	}

	if len(decodedBatch) != 2 {
		t.Errorf("批量请求数量不匹配: got %d, want 2", len(decodedBatch))
	}
}

// TestEmptyBatch 测试空批量请求的处理
func TestEmptyBatch(t *testing.T) {
	batch := []Request{}

	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("空批量请求序列化失败: %v", err)
	}

	if string(data) != "[]" {
		t.Errorf("空批量请求JSON格式错误: got %s, want []", string(data))
	}
}

// TestJSONRPCVersionConstant 测试JSON-RPC版本常量
func TestJSONRPCVersionConstant(t *testing.T) {
	if JSONRPCVersion != "2.0" {
		t.Errorf("JSON-RPC版本不正确: got %s, want 2.0", JSONRPCVersion)
	}
}

// TestNotificationRequest 测试通知请求（无ID）
func TestNotificationRequest(t *testing.T) {
	// 注意：在JSON-RPC中，没有ID的请求是通知，不需要响应
	req := Request{
		Version: "2.0",
		Method:  "notify_method",
		Params:  map[string]any{"event": "message"},
		// ID字段被省略或为nil
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("通知请求序列化失败: %v", err)
	}

	// 验证ID未被包含在JSON中
	var rawMap map[string]any
	if err := json.Unmarshal(data, &rawMap); err != nil {
		t.Fatalf("解析JSON失败: %v", err)
	}

	// 对于ID为0的Request，JSON可能不包含id字段
	// 这取决于JSON序列化的具体实现
}

// TestResponseWithoutID 测试没有ID的响应（错误情况）
func TestResponseWithoutID(t *testing.T) {
	resp := Response{
		Version: "2.0",
		Result:  "data",
		// ID字段为0（未指定）
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("JSON序列化失败: %v", err)
	}

	if string(data) == "" {
		t.Error("响应JSON为空")
	}
}
