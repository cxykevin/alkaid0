package pty

import (
	"runtime"
	"testing"
)

func TestNew(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 下需要 ConPTY 支持")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("Windows 下需要 ConPTY 支持")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("Windows 下需要 ConPTY 支持")
	}
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

// TTY 不做并发
// func TestConcurre
// ntReadWrite(t *testing.T) {
// 	if runtime.GOOS == "windows" {
// 		t.Skip("Windows 下需要 ConPTY 支持")
// 	}
// 	cfg := Config{Rows: 24, Cols: 80}

// 	p, slave, err := New(cfg)
// 	if err != nil {
// 		t.Fatalf("创建PTY失败: %v", err)
// 	}
// 	defer p.Close()
// 	defer slave.Close()

// 	// 启动goroutine写入数据
// 	go func() {
// 		data := []byte("hello world")
// 		_, err := slave.Write(data)
// 		if err != nil {
// 			t.Errorf("写入失败: %v", err)
// 		}
// 	}()

// 	// 并发读取
// 	buf := make([]byte, 1024)
// 	n, err := p.Read(buf)
// 	if err != nil {
// 		t.Fatalf("读取失败: %v", err)
// 	}
// 	if string(buf[:n]) != "hello world" {
// 		t.Errorf("读取数据不匹配: 期望 'hello world', 实际 '%s'", string(buf[:n]))
// 	}
// }
