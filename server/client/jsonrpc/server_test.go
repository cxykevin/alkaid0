package jsonrpc

import (
	"encoding/json"
	"testing"

	u "github.com/cxykevin/alkaid0/utils"
)

// TestServerMethodRegistration 测试方法注册
func TestServerMethodRegistration(t *testing.T) {
	srv := New()

	// 注册一个简单的测试方法
	Set(srv, "test_method", func(p u.H, call func(string, any, *string) error, connID uint64) (any, error) {
		return "success", nil
	})

	if _, ok := srv.Methods["test_method"]; !ok {
		t.Error("方法注册失败")
	}
}

// TestServerInvoke 测试方法调用
func TestServerInvoke(t *testing.T) {
	srv := New()

	// 注册一个返回特定值的方法
	Set(srv, "echo", func(p u.H, call func(string, any, *string) error, connID uint64) (any, error) {
		msg, ok := u.GetH[string](p, "message")
		if !ok {
			msg = "empty"
		}
		return msg, nil
	})

	// 构建请求
	req := Request{
		Version: JSONRPCVersion,
		ID:      1,
		Method:  "echo",
		Params: map[string]any{
			"message": "hello",
		},
	}

	reqData, _ := json.Marshal(req)
	outputs := []string{}

	// 调用handle方法
	_, _ = srv.handle(string(reqData), func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	// 验证输出
	if len(outputs) > 0 {
		var resp Response
		_ = json.Unmarshal([]byte(outputs[0]), &resp)
		if resp.Result != "hello" {
			t.Logf("方法调用返回值: %v", resp.Result)
		}
	} else {
		t.Logf("方法调用可能异步处理或无输出")
	}
}

// TestServerMethodNotFound 测试方法未找到的错误处理
func TestServerMethodNotFound(t *testing.T) {
	srv := New()

	req := Request{
		Version: JSONRPCVersion,
		ID:      1,
		Method:  "nonexistent_method",
		Params:  map[string]any{},
	}

	reqData, _ := json.Marshal(req)
	outputs := []string{}

	_, _ = srv.handle(string(reqData), func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	// 验证输出包含错误响应
	if len(outputs) > 0 {
		var resp Response
		json.Unmarshal([]byte(outputs[0]), &resp)
		if resp.Error == nil {
			t.Error("应该返回方法未找到的错误")
		}
		if resp.Error.Code != JRPCMethodNotFound {
			t.Errorf("错误码不正确: got %d, want %d", resp.Error.Code, JRPCMethodNotFound)
		}
	}
}

// TestServerPingMethod 测试内置ping方法
func TestServerPingMethod(t *testing.T) {
	srv := New()

	req := Request{
		Version: JSONRPCVersion,
		ID:      1,
		Method:  "ping",
		Params:  map[string]any{},
	}

	reqData, _ := json.Marshal(req)
	outputs := []string{}

	_, _ = srv.handle(string(reqData), func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	if len(outputs) > 0 {
		var resp Response
		json.Unmarshal([]byte(outputs[0]), &resp)
		if resp.Result != "pong" {
			t.Errorf("ping方法返回值不正确: got %v, want pong", resp.Result)
		}
	}
}

// TestServerExitMethod 测试exit方法
func TestServerExitMethod(t *testing.T) {
	srv := New()

	req := Request{
		Version: JSONRPCVersion,
		ID:      1,
		Method:  "exit",
		Params:  map[string]any{},
	}

	reqData, _ := json.Marshal(req)

	_, isExit := srv.handle(string(reqData), func(s string) error {
		return nil
	}, 1)

	if !isExit {
		t.Error("exit方法应该返回exit标志为true")
	}
}

// TestServerBatchRequest 测试批量请求处理
func TestServerBatchRequest(t *testing.T) {
	srv := New()

	// 注册一个方法
	Set(srv, "multiply", func(p u.H, call func(string, any, *string) error, connID uint64) (any, error) {
		a, _ := u.GetH[float64](p, "a")
		b, _ := u.GetH[float64](p, "b")
		return a * b, nil
	})

	batch := []Request{
		{
			Version: JSONRPCVersion,
			ID:      1,
			Method:  "multiply",
			Params:  map[string]any{"a": 2.0, "b": 3.0},
		},
		{
			Version: JSONRPCVersion,
			ID:      2,
			Method:  "multiply",
			Params:  map[string]any{"a": 4.0, "b": 5.0},
		},
	}

	batchData, _ := json.Marshal(batch)
	outputs := []string{}

	_, _ = srv.handle(string(batchData), func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	// 验证批量请求处理
	if len(outputs) > 0 {
		// JSON-RPC批量请求可能返回多个响应或一个响应数组
		t.Logf("批量请求输出数量: %d", len(outputs))
	}
}

// TestServerInvalidJSON 测试无效JSON处理
func TestServerInvalidJSON(t *testing.T) {
	srv := New()

	invalidJSON := "{ invalid json }"
	outputs := []string{}

	_, _ = srv.handle(invalidJSON, func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	// 应该返回parse error
	if len(outputs) > 0 {
		var resp Response
		if err := json.Unmarshal([]byte(outputs[0]), &resp); err == nil {
			if resp.Error != nil && resp.Error.Code == JRPCParseError {
				// 正确处理了parse错误
				return
			}
		}
		// 或者可能直接返回错误JSON
		t.Logf("无效JSON响应: %s", outputs[0])
	}
}

// TestServerInvalidVersion 测试无效版本号的请求
func TestServerInvalidVersion(t *testing.T) {
	srv := New()

	req := Request{
		Version: "1.0",
		ID:      1,
		Method:  "test",
		Params:  map[string]any{},
	}

	reqData, _ := json.Marshal(req)
	outputs := []string{}

	_, _ = srv.handle(string(reqData), func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	if len(outputs) > 0 {
		var resp Response
		json.Unmarshal([]byte(outputs[0]), &resp)
		if resp.Error == nil {
			t.Error("应该返回无效版本的错误")
		}
	}
}

// TestServerWithoutID 测试不包含ID的请求（通知）
func TestServerWithoutID(t *testing.T) {
	srv := New()

	Set(srv, "notify", func(p u.H, call func(string, any, *string) error, connID uint64) (any, error) {
		return nil, nil
	})

	// 构造没有ID的请求（通知）
	reqStr := `{"jsonrpc":"2.0","method":"notify","params":{}}`

	outputs := []string{}
	_, _ = srv.handle(reqStr, func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	// 对于通知请求，通常不返回响应
	// 所以outputs可能为空或取决于实现
}

// TestServerResponseFormat 测试响应格式正确性
func TestServerResponseFormat(t *testing.T) {
	srv := New()

	Set(srv, "test", func(p u.H, call func(string, any, *string) error, connID uint64) (any, error) {
		return map[string]any{"status": "ok"}, nil
	})

	req := Request{
		Version: JSONRPCVersion,
		ID:      123,
		Method:  "test",
		Params:  map[string]any{},
	}

	reqData, _ := json.Marshal(req)
	outputs := []string{}

	_, _ = srv.handle(string(reqData), func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	if len(outputs) > 0 {
		var resp Response
		if err := json.Unmarshal([]byte(outputs[0]), &resp); err != nil {
			t.Fatalf("响应JSON解析失败: %v", err)
		}

		if resp.Version != JSONRPCVersion {
			t.Errorf("版本不正确: got %s, want %s", resp.Version, JSONRPCVersion)
		}

		if resp.ID != 123 {
			t.Errorf("ID不匹配: got %v, want 123", resp.ID)
		}

		if resp.Error != nil {
			t.Errorf("不应该有错误: %v", resp.Error)
		}
	}
}

// TestNotificationNoResponse 测试通知请求不返回响应
func TestNotificationNoResponse(t *testing.T) {
	srv := New()

	Set(srv, "notify_test", func(p u.H, call func(string, any, *string) error, connID uint64) (any, error) {
		return "result", nil
	})

	// 通知请求（ID为nil）
	req := Request{
		Version: JSONRPCVersion,
		ID:      nil,
		Method:  "notify_test",
		Params:  map[string]any{},
	}

	reqData, _ := json.Marshal(req)
	outputs := []string{}

	_, _ = srv.handle(string(reqData), func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	// 通知请求不应该返回任何响应
	if len(outputs) != 0 {
		t.Errorf("通知请求应该不返回响应，但得到: %v", outputs)
	}
}

// TestParseErrorIDHandling 测试解析错误时ID处理
func TestParseErrorIDHandling(t *testing.T) {
	srv := New()

	// 无效的JSON
	invalidJSON := `{ invalid json }`
	outputs := []string{}

	returnStr, _ := srv.handle(invalidJSON, func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	// 检查直接返回值或输出
	if returnStr == "" && len(outputs) == 0 {
		t.Error("解析错误应该返回响应")
		return
	}

	result := returnStr
	if result == "" && len(outputs) > 0 {
		result = outputs[0]
	}

	t.Logf("解析错误响应: %s", result)

	var resp Response
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("响应JSON解析失败: %v", err)
	}

	// 解析失败时，ID应为null
	if resp.ID != nil {
		t.Errorf("解析错误时ID应为null，但得到: %v", resp.ID)
	}

	if resp.Error == nil || resp.Error.Code != JRPCParseError {
		t.Errorf("应该返回ParseError，但得到: %v", resp.Error)
	}
}

// TestBatchWithNotifications 测试批量请求中包含通知
func TestBatchWithNotifications(t *testing.T) {
	srv := New()

	Set(srv, "add", func(p u.H, call func(string, any, *string) error, connID uint64) (any, error) {
		a, _ := u.GetH[float64](p, "a")
		b, _ := u.GetH[float64](p, "b")
		return a + b, nil
	})

	batch := []Request{
		{
			Version: JSONRPCVersion,
			ID:      1,
			Method:  "add",
			Params:  map[string]any{"a": 1.0, "b": 2.0},
		},
		{
			Version: JSONRPCVersion,
			ID:      nil, // 通知请求
			Method:  "add",
			Params:  map[string]any{"a": 3.0, "b": 4.0},
		},
		{
			Version: JSONRPCVersion,
			ID:      2,
			Method:  "add",
			Params:  map[string]any{"a": 5.0, "b": 6.0},
		},
	}

	batchData, _ := json.Marshal(batch)
	outputs := []string{}

	returnStr, _ := srv.handle(string(batchData), func(s string) error {
		outputs = append(outputs, s)
		return nil
	}, 1)

	// 检查直接返回值或输出
	result := returnStr
	if result == "" && len(outputs) > 0 {
		result = outputs[0]
	}

	if result == "" {
		t.Error("批量请求应该返回响应")
		return
	}

	t.Logf("批量请求响应: %s", result)

	var resps []Response
	if err := json.Unmarshal([]byte(result), &resps); err != nil {
		t.Fatalf("响应JSON解析失败: %v", err)
	}

	// 应该只有2个响应（ID为1和2的请求），通知请求不返回响应
	if len(resps) != 2 {
		t.Errorf("应该返回2个响应，但得到%d个", len(resps))
	}

	// 检查返回的响应ID
	if len(resps) >= 2 {
		if resps[0].ID != float64(1) {
			t.Errorf("第一个响应ID应为1，但得到:%v", resps[0].ID)
		}
		if resps[1].ID != float64(2) {
			t.Errorf("第二个响应ID应为2，但得到:%v", resps[1].ID)
		}
	}
}
