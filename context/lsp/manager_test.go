package lsp

import (
	"encoding/json"
	"testing"
	"time"
)

// TestReapIdleShutdownContextAlive 验证 reapIdle 传给 Shutdown 的 context 在 goroutine 执行期间有效：
// 修复前 reapIdle 用 defer cancel()，函数一返回 context 就被取消，
// Shutdown 会在收到服务器响应前放弃等待。
func TestReapIdleShutdownContextAlive(t *testing.T) {
	transport, mock := newMockTransport()
	defer transport.Close()
	defer mock.close()

	client := NewClient(t.TempDir(), "go", LanguageServerConfig{})
	client.transport = transport
	client.state = StateReady
	client.lastUsed = time.Now().Add(-time.Hour)

	m := &Manager{
		clients:     map[string]*Client{"wd|go": client},
		failCount:   map[string]int{},
		idleTimeout: time.Second,
	}

	m.reapIdle()

	req := mock.nextRequest(3 * time.Second)
	if req == nil {
		t.Fatal("未收到 shutdown 请求")
	}
	if req["method"] != "shutdown" {
		t.Fatalf("method = %v, want shutdown", req["method"])
	}

	// 尚未回复 shutdown 时客户端应仍在等待：不能已经放弃并发出 exit
	select {
	case body, ok := <-mock.reqBody:
		if !ok {
			t.Fatal("回收过程中连接被提前关闭")
		}
		var msg map[string]any
		_ = json.Unmarshal(body, &msg)
		if msg["method"] == "exit" {
			t.Fatal("shutdown 未收到响应就发送了 exit：传给 Shutdown 的 context 已被取消")
		}
	case <-time.After(300 * time.Millisecond):
		// 期望路径：shutdown 请求仍在等待响应
	}

	id, _ := req["id"].(float64)
	mock.respond(int64(id), nil)

	exit := mock.nextRequest(3 * time.Second)
	if exit == nil || exit["method"] != "exit" {
		t.Fatalf("shutdown 响应后未收到 exit 通知: %v", exit)
	}
}
