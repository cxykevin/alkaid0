package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// mockLSP 模拟 LSP 服务器端
type mockLSP struct {
	stdinReader  *io.PipeReader
	stdoutWriter *io.PipeWriter
	reqBody      chan []byte // 收到的请求 body
}

// newMockTransport 创建传输层和对应的模拟服务器端
// io.Pipe() 返回 (*PipeReader, *PipeWriter)
// io.Pipe 的 Write 会阻塞直到对应的 Read 消费数据，
// 因此始终启动后台 readLoop 消费 stdin
func newMockTransport() (*Transport, *mockLSP) {
	serverStdinReader, clientStdinWriter := io.Pipe()
	clientStdoutReader, serverStdoutWriter := io.Pipe()

	transport := NewTransport(clientStdinWriter, clientStdoutReader)
	mock := &mockLSP{
		stdinReader:  serverStdinReader,
		stdoutWriter: serverStdoutWriter,
		reqBody:      make(chan []byte, 64),
	}

	go mock.stdinReadLoop()

	return transport, mock
}

// stdinReadLoop 后台读取 stdin 帧并推送到 reqBody 通道
func (m *mockLSP) stdinReadLoop() {
	defer close(m.reqBody)
	for {
		body, err := m.readFrame()
		if err != nil {
			return
		}
		b := make([]byte, len(body))
		copy(b, body)
		m.reqBody <- b
	}
}

// readFrame 读取一个 Content-Length 帧，返回 body
func (m *mockLSP) readFrame() ([]byte, error) {
	var length int
	for {
		line, err := m.readLine()
		if err != nil {
			return nil, err
		}
		const prefix = "Content-Length: "
		if len(line) >= len(prefix) && string(line[:len(prefix)]) == prefix {
			fmt.Sscanf(string(line), prefix+"%d", &length)
			break
		}
	}
	// 跳过空行
	m.readLine()

	body := make([]byte, length)
	if _, err := io.ReadFull(m.stdinReader, body); err != nil {
		return nil, err
	}
	return body, nil
}

// readLine 读一行（直到 \n），返回字节（不含 \r\n）
func (m *mockLSP) readLine() ([]byte, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		_, err := m.stdinReader.Read(b)
		if err != nil {
			return buf, err
		}
		if b[0] == '\n' {
			break
		}
		if b[0] != '\r' {
			buf = append(buf, b[0])
		}
	}
	return buf, nil
}

// nextRequest 阻塞等待下一个请求（带超时）
func (m *mockLSP) nextRequest(timeout time.Duration) map[string]any {
	select {
	case body, ok := <-m.reqBody:
		if !ok {
			return nil
		}
		var parsed map[string]any
		json.Unmarshal(body, &parsed)
		return parsed
	case <-time.After(timeout):
		return nil
	}
}

// writeRaw 向 stdout 写入原始数据（用于构造服务器主动请求等特殊帧）
func (m *mockLSP) writeRaw(raw string) {
	m.stdoutWriter.Write([]byte(raw))
}

// writeFrame 向 stdout 写入一个带 Content-Length 头的帧
func (m *mockLSP) writeFrame(body string) {
	m.writeRaw(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body))
}

// respond 向 stdout 写入 JSON-RPC 响应帧
func (m *mockLSP) respond(id int64, result any) {
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(data), string(data))
	m.stdoutWriter.Write([]byte(msg))
}

// close 关闭管道
func (m *mockLSP) close() {
	m.stdinReader.Close()
	m.stdoutWriter.Close()
}

// ---------------------------------------------------------------------------
// 基础功能测试
// ---------------------------------------------------------------------------

func TestTransportSendNotification(t *testing.T) {
	transport, mock := newMockTransport()
	defer transport.Close()
	defer mock.close()

	err := transport.SendNotification("test/notify", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	req := mock.nextRequest(3 * time.Second)
	if req == nil {
		t.Fatal("did not receive notification")
	}
	if _, hasID := req["id"]; hasID {
		t.Error("notification should not have id field")
	}
	if req["method"] != "test/notify" {
		t.Errorf("method = %v, want test/notify", req["method"])
	}
}

func TestTransportSendRequestWithResponse(t *testing.T) {
	transport, mock := newMockTransport()
	defer transport.Close()
	defer mock.close()

	// 启动回复 goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := mock.nextRequest(10 * time.Second)
		if req == nil {
			return
		}
		id, _ := req["id"].(float64)
		mock.respond(int64(id), "hello")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := transport.SendRequest(ctx, "test/echo", map[string]string{"msg": "ping"})
	if err != nil {
		t.Fatalf("SendRequest failed: %v", err)
	}
	var str string
	json.Unmarshal(result, &str)
	if str != "hello" {
		t.Fatalf("got %q, want %q", str, "hello")
	}
	<-done
}

func TestTransportMultipleRequests(t *testing.T) {
	transport, mock := newMockTransport()
	defer transport.Close()
	defer mock.close()

	go func() {
		for i := range 3 {
			req := mock.nextRequest(10 * time.Second)
			if req == nil {
				return
			}
			id, _ := req["id"].(float64)
			mock.respond(int64(id), fmt.Sprintf("r%d", i+1))
		}
	}()

	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		result, err := transport.SendRequest(ctx, "test/method", nil)
		cancel()
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		var str string
		json.Unmarshal(result, &str)
		if str != fmt.Sprintf("r%d", i) {
			t.Errorf("request %d: got %q, want r%d", i, str, i)
		}
	}
}

func TestTransportTimeout(t *testing.T) {
	transport, mock := newMockTransport()
	defer transport.Close()
	defer mock.close()

	// 不回复，让请求超时
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := transport.SendRequest(ctx, "test/timeout", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestTransportClose(t *testing.T) {
	transport, mock := newMockTransport()
	defer mock.close()

	transport.Close()

	ctx := context.Background()
	_, err := transport.SendRequest(ctx, "test/afterclose", nil)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

// ---------------------------------------------------------------------------
// 回归测试：读循环 EOF 必须关闭传输层，不能空转
// ---------------------------------------------------------------------------

func TestTransportReadLoopEOFClosesTransport(t *testing.T) {
	transport, mock := newMockTransport()
	defer mock.close()

	// 模拟 LSP 服务器进程退出：stdout 读到 EOF
	mock.stdoutWriter.Close()

	select {
	case <-transport.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("readLoop 收到 EOF 后未关闭传输层（readLoop 在已 EOF 的管道上空转）")
	}

	if transport.HasPending() {
		t.Error("传输层关闭后仍存在 pending 请求")
	}
}

// ---------------------------------------------------------------------------
// 回归测试：服务器主动请求不能当作响应投递给 pending
// ---------------------------------------------------------------------------

func TestTransportServerRequestIsNotResponse(t *testing.T) {
	transport, mock := newMockTransport()
	defer transport.Close()
	defer mock.close()

	go func() {
		req := mock.nextRequest(5 * time.Second)
		if req == nil {
			return
		}
		id := int64(req["id"].(float64))
		// 服务器用相同 id 主动发起请求：修复前会被当成响应吞掉，使在途请求返回 nil 结果
		mock.writeFrame(fmt.Sprintf(
			"{\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":\"workspace/configuration\",\"params\":{\"items\":[]}}", id))
		time.Sleep(150 * time.Millisecond)
		mock.respond(id, "hello")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := transport.SendRequest(ctx, "test/echo", nil)
	if err != nil {
		t.Fatalf("SendRequest failed: %v", err)
	}
	var got string
	if err := json.Unmarshal(result, &got); err != nil || got != "hello" {
		t.Fatalf("result = %q (unmarshal err=%v), want %q（服务器请求被误当作响应）", string(result), err, "hello")
	}
}

func TestTransportServerRequestGetsReply(t *testing.T) {
	transport, mock := newMockTransport()
	defer transport.Close()
	defer mock.close()

	// 字符串 id 的服务器请求：必须回复，否则服务器会一直等待
	mock.writeFrame("{\"jsonrpc\":\"2.0\",\"id\":\"srv-1\",\"method\":\"window/workDoneProgress/create\",\"params\":{\"token\":\"t\"}}")

	reply := mock.nextRequest(3 * time.Second)
	if reply == nil {
		t.Fatal("服务器主动请求未收到任何回复")
	}
	if id, _ := reply["id"].(string); id != "srv-1" {
		t.Fatalf("回复 id = %v, want srv-1", reply["id"])
	}
	if _, hasResult := reply["result"]; !hasResult {
		t.Fatalf("回复缺少 result 字段: %v", reply)
	}
}

// ---------------------------------------------------------------------------
// 回归测试：Content-Length 上限、消息体损坏不应拖垮连接
// ---------------------------------------------------------------------------

func TestTransportContentLengthLimit(t *testing.T) {
	transport, mock := newMockTransport()
	defer mock.close()

	// 超过上限的 Content-Length 应被拒绝并关闭连接，而不是直接分配内存
	mock.writeRaw(fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessageSize+1))

	select {
	case <-transport.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("超大 Content-Length 未终止传输层")
	}
}

func TestTransportInvalidJSONKeepsConnection(t *testing.T) {
	transport, mock := newMockTransport()
	defer transport.Close()
	defer mock.close()

	go func() {
		req := mock.nextRequest(5 * time.Second)
		if req == nil {
			return
		}
		id := int64(req["id"].(float64))
		mock.respond(id, "ok")
	}()

	// 帧边界完整但内容非法：只应跳过该消息，不应关闭连接
	mock.writeFrame("{bad json")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := transport.SendRequest(ctx, "test/ping", nil); err != nil {
		t.Fatalf("非法 JSON 消息后连接不可用: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 回归测试：并发写必须串行，不得交错 header/body
// ---------------------------------------------------------------------------

// overlapWriter 记录 Write 调用的最大并发度
type overlapWriter struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (w *overlapWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.active++
	if w.active > w.maxActive {
		w.maxActive = w.active
	}
	w.mu.Unlock()

	// 让出时间片，放大并发写交错窗口
	time.Sleep(2 * time.Millisecond)

	w.mu.Lock()
	w.active--
	w.mu.Unlock()
	return len(p), nil
}

func (w *overlapWriter) Close() error { return nil }

func (w *overlapWriter) max() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.maxActive
}

func TestTransportConcurrentWritesSerialized(t *testing.T) {
	stdoutReader, stdoutWriter := io.Pipe()
	defer stdoutWriter.Close()

	w := &overlapWriter{}
	transport := NewTransport(w, stdoutReader)
	defer transport.Close()

	const writers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := transport.SendNotification("test/notify", map[string]int{"i": i}); err != nil {
				t.Errorf("SendNotification %d failed: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if got := w.max(); got > 1 {
		t.Fatalf("检测到 %d 个并发 Write：header 与 body 未作为整体串行写入，帧会被交错写坏", got)
	}
}
