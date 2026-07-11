package pty

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}

	p, f, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer p.Close()

	if p == nil {
		t.Fatal("PTY为nil")
	}
	if f == nil {
		t.Fatal("返回的文件描述符为nil")
	}
	if p.File() != f {
		t.Fatal("PTY.File() 与返回文件描述符不一致")
	}
}

func TestResizeAndGetSize(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}

	p, _, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer p.Close()

	if err := p.Resize(30, 100); err != nil {
		t.Fatalf("调整大小失败: %v", err)
	}

	rows, cols, err := p.GetSize()
	if err != nil {
		t.Fatalf("获取大小失败: %v", err)
	}
	if rows != 30 || cols != 100 {
		t.Fatalf("终端大小不匹配: %dx%d", rows, cols)
	}
}

func TestCloseMultipleTimes(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}

	p, _, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("第一次关闭失败: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Logf("第二次关闭返回错误（可接受）: %v", err)
	}
}

func TestNewDefaultConfig(t *testing.T) {
	// 零值配置应使用默认值
	p, _, err := New(Config{})
	if err != nil {
		t.Fatalf("使用零值配置创建PTY失败: %v", err)
	}
	defer p.Close()

	rows, cols, err := p.GetSize()
	if err != nil {
		t.Fatalf("获取大小失败: %v", err)
	}
	if rows != 24 {
		t.Errorf("默认行数应为24，实际 %d", rows)
	}
	if cols != 80 {
		t.Errorf("默认列数应为80，实际 %d", cols)
	}
}

func TestWrite(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}

	p, _, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer p.Close()

	// 写入 PTY（写入 master，数据进入内核缓冲区）
	msg := []byte("hello pty")
	n, err := p.Write(msg)
	if err != nil {
		t.Fatalf("写入PTY失败: %v", err)
	}
	if n != len(msg) {
		t.Errorf("写入长度 %d，期望 %d", n, len(msg))
	}
}

func TestWriteEmpty(t *testing.T) {
	p, _, err := New(Config{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer p.Close()

	n, err := p.Write([]byte{})
	if err != nil {
		t.Fatalf("写入空数据失败: %v", err)
	}
	if n != 0 {
		t.Errorf("空写入长度应为0，实际 %d", n)
	}
}

func TestReadOnClosed(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}
	p, _, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	p.Close()

	buf := make([]byte, 10)
	_, err = p.Read(buf)
	if err == nil {
		t.Error("在已关闭的PTY上读取应返回错误")
	}
}

func TestWriteOnClosed(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}
	p, _, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	p.Close()

	_, err = p.Write([]byte("test"))
	if err == nil {
		t.Error("在已关闭的PTY上写入应返回错误")
	}
}

func TestResizeOnClosed(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}
	p, _, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	p.Close()

	err = p.Resize(80, 24)
	if err == nil {
		t.Error("在已关闭的PTY上调整大小应返回错误")
	}
}

func TestGetSizeOnClosed(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}
	p, _, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	p.Close()

	_, _, err = p.GetSize()
	if err == nil {
		t.Error("在已关闭的PTY上获取大小应返回错误")
	}
}

func TestReadStderr(t *testing.T) {
	// ReadStderr 委托给 Read，在无子进程时返回 EIO
	// 这里只验证接口可用且不 panic
	p, _, err := New(Config{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer p.Close()

	_, err = p.ReadStderr(nil)
	if err == nil {
		t.Log("ReadStderr with nil buffer returned nil (expected with no slave)")
	}
}

func TestFileAfterClose(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}
	p, _, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	f := p.File()
	p.Close()

	// File() 应仍返回 fd（只是已关闭）
	if f == nil {
		t.Error("Close 后 File() 不应返回 nil")
	}
}

func TestCopyToWithoutSlave(t *testing.T) {
	// CopyTo 在无从端子进程时会阻塞，测试关闭时能正常退出
	p, _, err := New(Config{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- p.CopyTo(&buf)
	}()

	time.Sleep(50 * time.Millisecond)
	p.Close()

	select {
	case err := <-done:
		if err != nil && !strings.Contains(err.Error(), "closed") &&
			!strings.Contains(err.Error(), "input/output error") {
			t.Errorf("CopyTo unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CopyTo超时未退出")
	}
}

