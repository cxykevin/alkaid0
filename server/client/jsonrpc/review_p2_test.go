package jsonrpc

import (
	"encoding/json"
	"strings"
	"testing"

	u "github.com/cxykevin/alkaid0/utils"
)

// TestInvalidRequestEchoesRecoverableID 回归：JSON 合法但无法按请求结构解码
// （字段类型错误）时，id 仍可确定，必须回显 id 并返回 Invalid Request，
// 而不是丢成 Parse error + id=null。
func TestInvalidRequestEchoesRecoverableID(t *testing.T) {
	srv := New()
	ret, _ := srv.handle(`{"jsonrpc":"2.0","id":7,"method":123}`, func(string) error { return nil }, 1)
	if ret == "" {
		t.Fatal("应返回错误响应")
	}
	if !strings.Contains(ret, `"id":7`) {
		t.Errorf("响应必须回显可确定的 id=7，got %s", ret)
	}
	var resp Response
	if err := json.Unmarshal([]byte(ret), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != JRPCInvalidRequest {
		t.Errorf("应返回 InvalidRequest(-32600)，got %#v", resp.Error)
	}
}

// TestParseErrorResponseHasNullID 回归：真正无法恢复 id 的解析错误，
// 响应必须携带 "id":null（旧实现 ID 带 omitempty，字段被整个省略）。
func TestParseErrorResponseHasNullID(t *testing.T) {
	srv := New()
	ret, _ := srv.handle(`{"jsonrpc":"2.0","id":`, func(string) error { return nil }, 1)
	if ret == "" {
		t.Fatal("应返回错误响应")
	}
	if !strings.Contains(ret, `"id":null`) {
		t.Errorf("解析错误响应必须包含 id:null，got %s", ret)
	}
	var resp Response
	if err := json.Unmarshal([]byte(ret), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != JRPCParseError {
		t.Errorf("应返回 ParseError(-32700)，got %#v", resp.Error)
	}
	if resp.ID != nil {
		t.Errorf("解析错误时 id 应为 null，got %v", resp.ID)
	}
}

// TestTypedRPCErrorCodePreserved handler 返回 *RPCError 时必须使用其错误码，
// 而不是全部映射成 -32099。
func TestTypedRPCErrorCodePreserved(t *testing.T) {
	srv := New()
	Set(srv, "boom", func(_ u.H, _ func(string, any, *string) error, _ uint64) (any, error) {
		return nil, NewRPCError(JRPCInvalidParams, "bad argument")
	})
	ret, _ := srv.handle(`{"jsonrpc":"2.0","id":1,"method":"boom","params":{}}`, func(string) error { return nil }, 1)
	var resp Response
	if err := json.Unmarshal([]byte(ret), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != JRPCInvalidParams {
		t.Errorf("应使用 handler 指定的 -32602，got %#v", resp.Error)
	}
}

// TestInvalidParamsCode 参数解码失败必须返回 -32602，而不是 -32099。
func TestInvalidParamsCode(t *testing.T) {
	type needArgs struct {
		N int `json:"n"`
	}
	srv := New()
	Set(srv, "needn", func(r needArgs, _ func(string, any, *string) error, _ uint64) (any, error) {
		return r.N, nil
	})
	ret, _ := srv.handle(`{"jsonrpc":"2.0","id":1,"method":"needn","params":{"n":"not-a-number"}}`, func(string) error { return nil }, 1)
	var resp Response
	if err := json.Unmarshal([]byte(ret), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != JRPCInvalidParams {
		t.Errorf("参数解码失败应返回 -32602，got %#v", resp.Error)
	}
}
