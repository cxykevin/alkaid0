//go:build windows

package pty

import "testing"

func TestNewWindows(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}

	p, f, err := New(cfg)
	if err != nil {
		t.Skipf("ConPTY 不可用: %v", err)
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

func TestResizeAndGetSizeWindows(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}

	p, _, err := New(cfg)
	if err != nil {
		t.Skipf("ConPTY 不可用: %v", err)
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

func TestCloseMultipleTimesWindows(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}

	p, _, err := New(cfg)
	if err != nil {
		t.Skipf("ConPTY 不可用: %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("第一次关闭失败: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Logf("第二次关闭返回错误（可接受）: %v", err)
	}
}
