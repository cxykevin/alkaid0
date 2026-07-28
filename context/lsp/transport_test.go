package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
