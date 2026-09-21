package lsp

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/log"
)

// TestClientClosedOnTransportEOF 验证 LSP 进程退出（stdout EOF）后客户端不再保持 Ready，
// 否则 manager 会一直复用已死的客户端，后续请求只能等到 ctx 超时。
func TestClientClosedOnTransportEOF(t *testing.T) {
	transport, mock := newMockTransport()
	defer mock.close()

	client := NewClient(t.TempDir(), "go", LanguageServerConfig{})
	client.attachTransport(transport)
	client.state = StateReady

	// 模拟语言服务器进程退出
	mock.stdoutWriter.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && client.State() != StateClosed {
		time.Sleep(10 * time.Millisecond)
	}
	if got := client.State(); got != StateClosed {
		t.Fatalf("client state = %s, want closed（进程已退出仍被当作 Ready 复用）", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := client.SendRequest(ctx, "test/method", nil); err == nil {
		t.Fatal("进程退出后 SendRequest 应快速失败")
	}
}

// TestClientClosedBeforeReady 验证 initialize 完成前传输层断开同样标记 Closed
func TestClientClosedBeforeReady(t *testing.T) {
	transport, mock := newMockTransport()
	defer mock.close()

	client := NewClient(t.TempDir(), "go", LanguageServerConfig{})
	client.attachTransport(transport)
	client.state = StateCreated

	mock.stdoutWriter.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && client.State() != StateClosed {
		time.Sleep(10 * time.Millisecond)
	}
	if got := client.State(); got != StateClosed {
		t.Fatalf("client state = %s, want closed（启动阶段的断开未被标记不可用）", got)
	}
}

// TestMarkReadyAfterTransportClosed 验证传输层已断开时不允许把客户端标记为 Ready
func TestMarkReadyAfterTransportClosed(t *testing.T) {
	transport, mock := newMockTransport()
	defer mock.close()

	client := NewClient(t.TempDir(), "go", LanguageServerConfig{})
	client.attachTransport(transport)
	client.state = StateCreated

	mock.stdoutWriter.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && client.State() != StateClosed {
		time.Sleep(10 * time.Millisecond)
	}

	if err := client.markReady(); err == nil {
		t.Fatal("传输层已关闭时 markReady 应返回错误")
	}
	if got := client.State(); got == StateReady {
		t.Fatal("传输层已关闭的客户端不应处于 Ready")
	}
}

// TestReadStderrLongLine 验证单行超过 64KiB 时仍继续排空 stderr。
// 默认 Scanner 上限会在超长行处终止扫描，管道不再被读取，持续写 stderr 的语言服务器可能因此阻塞。
func TestReadStderrLongLine(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	client := &Client{language: "go", logger: log.New("lsp:test")}
	done := make(chan struct{})
	go func() {
		client.readStderr(pr)
		close(done)
	}()

	writeErr := make(chan error, 1)
	go func() {
		_, err := pw.Write([]byte(strings.Repeat("x", 256*1024) + "\nafter\n"))
		writeErr <- err
	}()

	select {
	case err := <-writeErr:
		if err != nil {
			t.Fatalf("写入 stderr 失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readStderr 在单行超过 64KiB 后停止排空 stderr")
	}

	pw.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stderr 关闭后 readStderr 未退出")
	}
}
