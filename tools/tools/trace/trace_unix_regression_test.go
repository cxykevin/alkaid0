//go:build unix

package trace

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestTraceRejectsFIFOWithoutBlocking 回归：read 读取前必须校验常规文件，
// 否则 os.ReadFile 会在 FIFO 上永久阻塞（工具调用挂死）。
func TestTraceRejectsFIFOWithoutBlocking(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pipe")); err != nil {
		t.Fatal(err)
	}

	session := &structs.Chats{
		ID:                   1,
		DB:                   db,
		Root:                 dir,
		TemporyDataOfSession: make(map[string]any),
		NowAgent:             "test_agent",
	}

	path := any("pipe")
	type traceResult struct {
		result map[string]*any
		err    error
	}
	done := make(chan traceResult, 1)
	go func() {
		_, _, ret, err := Trace(session, map[string]*any{"path": &path}, []*any{})
		done <- traceResult{ret, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Trace: %v", r.err)
		}
		if success, ok := (*r.result["success"]).(bool); !ok || success {
			t.Fatalf("FIFO 必须读取失败, result=%#v", r.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read 在 FIFO 上阻塞：读取前必须校验常规文件")
	}
}
