//go:build unix

package edit

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestWriteFile_RejectsFIFOWithoutBlocking 回归：FIFO 不是常规文件，edit 读取前
// 必须用 Lstat/Stat 拦截。旧实现直接 os.Open + bufio.Scanner，会永久阻塞且无法取消。
func TestWriteFile_RejectsFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	session := newEditSession(t, dir)
	mp := map[string]*any{"path": ptr("pipe"), "target": ptr("@all"), "text": ptr("x")}

	type editResult struct {
		ret map[string]*any
		err error
	}
	done := make(chan editResult, 1)
	go func() {
		_, _, ret, err := writeFile(session, mp, nil)
		done <- editResult{ret, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("writeFile: %v", r.err)
		}
		assertEditFailed(t, r.ret)
	case <-time.After(5 * time.Second):
		t.Fatal("writeFile 在 FIFO 上阻塞：读取前必须校验常规文件")
	}
}
