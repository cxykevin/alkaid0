//go:build unix

package actions

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestFsWriteFIFODoesNotBlock 回归：旧实现以 O_WRONLY|O_CREATE|O_TRUNC 打开目标，
// 对 FIFO 会在 open 上永久阻塞（无读端）；200ms 超时只能让调用方提前报错，
// goroutine 仍卡在 open 且原内容已被截断。临时文件 + rename 方案直接替换 FIFO，
// 不会阻塞。
func TestFsWriteFIFODoesNotBlock(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := registerTestSession(t, tmpDir, 1)
	fifo := filepath.Join(tmpDir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := FsWrite(FsWriteRequest{SessionID: sessionID, Path: "pipe", Content: "hello"}, nil, 1)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("覆盖写 FIFO 不应阻塞或超时: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fs/write 卡在 FIFO 的 open 上（旧实现 O_WRONLY 打开 FIFO 永久阻塞）")
	}

	data, err := os.ReadFile(fifo)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want hello", data)
	}
}
