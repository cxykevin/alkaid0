//go:build !windows

package pty

import "testing"

func TestOpen(t *testing.T) {
	cfg := Config{Rows: 24, Cols: 80}
	master, slave, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open PTY失败: %v", err)
	}
	if master == nil {
		t.Fatal("master fd为nil")
	}
	if slave == nil {
		t.Fatal("slave fd为nil")
	}
	master.Close()
	slave.Close()
}

func TestOpenDefaultConfig(t *testing.T) {
	master, slave, err := Open(Config{})
	if err != nil {
		t.Fatalf("使用零值配置 Open PTY失败: %v", err)
	}
	master.Close()
	slave.Close()
}
