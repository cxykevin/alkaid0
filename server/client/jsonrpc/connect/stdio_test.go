package connect

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer 是并发安全的输出缓冲：startStdio 在多个处理协程里写输出，
// 测试主协程需要同时（在同步点之后）读取，裸 bytes.Buffer 会构成数据竞争。
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitLines 等到输出中出现 n 行（或超时）。
func (b *lockedBuffer) waitLines(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := b.String(); len(nonEmptyLines(got)) >= n {
			return nonEmptyLines(got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nonEmptyLines(b.String())
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestStartStdio_OneMessagePerLine：一条非空行 = 一条消息；空行与纯空白行被忽略；
// 每条消息的响应单独占一行；读取结束后必须通知 closeConn。
func TestStartStdio_OneMessagePerLine(t *testing.T) {
	in := strings.NewReader("{\"id\":1}\n\n   \n{\"id\":2}\n")
	out := &lockedBuffer{}
	calls := make(chan string, 8)
	handler := func(msg string, _ func(string) error, connID uint64) (string, bool) {
		if connID != 1 {
			calls <- "bad-conn-id"
			return "", false
		}
		calls <- msg
		return "resp:" + msg, false
	}
	closed := make(chan uint64, 1)
	startStdio(in, out, handler, func(id uint64) { closed <- id })

	seen := map[string]bool{}
	for range 2 {
		select {
		case m := <-calls:
			seen[m] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("handler 未被调用两次，已收到 %v", seen)
		}
	}
	if !seen["{\"id\":1}"] || !seen["{\"id\":2}"] {
		t.Errorf("handler 收到的消息不对: %v", seen)
	}
	if seen["bad-conn-id"] {
		t.Error("connID 应为 1")
	}

	select {
	case id := <-closed:
		if id != 1 {
			t.Errorf("closeConn id = %d, want 1", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EOF 后未调用 closeConn")
	}

	lines := out.waitLines(t, 2)
	if len(lines) != 2 {
		t.Fatalf("应写回两行响应（一行一条消息），got %q", lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "resp:{\"id\":1}") || !strings.Contains(joined, "resp:{\"id\":2}") {
		t.Errorf("响应内容不对: %q", joined)
	}

	// 空行/空白行不得产生额外消息
	select {
	case m := <-calls:
		t.Errorf("空行不应产生消息，却收到 %q", m)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestStartStdio_LastLineWithoutTrailingNewline 回归：客户端写完最后一条消息
// （末尾没有换行）就关闭 stdin 时，这条消息不能被丢掉。
//
// 背景：ReadString 在 EOF 时会连同已读内容一起返回，旧实现在 err != nil 时直接
// break，于是最后一条无换行的消息永远不会交给 handler。
func TestStartStdio_LastLineWithoutTrailingNewline(t *testing.T) {
	calls := make(chan string, 2)
	handler := func(msg string, _ func(string) error, _ uint64) (string, bool) {
		calls <- msg
		return "ok", false
	}
	closed := make(chan uint64, 1)
	startStdio(strings.NewReader("{\"id\":7}"), &lockedBuffer{}, handler, func(id uint64) { closed <- id })

	select {
	case m := <-calls:
		if m != "{\"id\":7}" {
			t.Errorf("最后一条消息内容不对: %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EOF 前最后一条（无结尾换行）消息被丢弃")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("EOF 后未调用 closeConn")
	}
}

// TestStartStdio_WriteCallbackThenResponse：handler 通过回调推送的分片先于
// 最终响应写出，且各自占一行（stdio 传输以换行分帧）。
func TestStartStdio_WriteCallbackThenResponse(t *testing.T) {
	out := &lockedBuffer{}
	handler := func(_ string, write func(string) error, _ uint64) (string, bool) {
		if err := write("{\"partial\":true}"); err != nil {
			t.Errorf("write 回调失败: %v", err)
		}
		return "{\"id\":1,\"result\":null}", false
	}
	closed := make(chan uint64, 1)
	startStdio(strings.NewReader("go\n"), out, handler, func(id uint64) { closed <- id })

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("EOF 后未调用 closeConn")
	}
	lines := out.waitLines(t, 2)
	if len(lines) != 2 {
		t.Fatalf("应写出分片与响应两行，got %q", lines)
	}
	if lines[0] != "{\"partial\":true}" {
		t.Errorf("第一行应为分片，got %q", lines[0])
	}
	if lines[1] != "{\"id\":1,\"result\":null}" {
		t.Errorf("第二行应为最终响应，got %q", lines[1])
	}
}

// TestStartStdio_ExitRequest：handler 返回 exit=true 时必须请求退出（stdio 模式下
// 进程随连接结束）。
func TestStartStdio_ExitRequest(t *testing.T) {
	exitCodes := make(chan int, 1)
	restore := stdioExit
	stdioExit = func(code int) { exitCodes <- code }
	defer func() { stdioExit = restore }()

	handler := func(_ string, _ func(string) error, _ uint64) (string, bool) {
		return "", true
	}
	startStdio(strings.NewReader("bye\n"), &lockedBuffer{}, handler, func(uint64) {})

	select {
	case code := <-exitCodes:
		if code != 0 {
			t.Errorf("退出码 = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shouldExit=true 未触发退出")
	}
}
