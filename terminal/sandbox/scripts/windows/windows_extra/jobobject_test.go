//go:build windows

package windows

import (
	"os"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// TestJobObjectConcurrentCloseAndTerminate 回归测试：Close（Wait/Clean 路径）与
// Terminate/Assign（Kill 路径）可能来自不同 goroutine。旧实现直接并发读写
// handle 字段：-race 下报数据竞争，且 Close 之后仍可能用已关闭（甚至被系统
// 复用）的句柄调用 API。修复后所有访问都由 mu 串行化。
func TestJobObjectConcurrentCloseAndTerminate(t *testing.T) {
	// 用一个真实但权限不足的进程句柄：Assign 会走到加锁读取 handle 的逻辑，
	// 因缺少 PROCESS_SET_QUOTA 而失败，不会真的把测试进程加入 Job。
	me, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("OpenProcess(self) failed: %v", err)
	}
	defer windows.CloseHandle(me)

	for i := 0; i < 200; i++ {
		job, err := NewJobObject()
		if err != nil {
			t.Fatalf("NewJobObject failed: %v", err)
		}
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); _ = job.Terminate() }()
		go func() { defer wg.Done(); _ = job.Assign(me) }()
		go func() { defer wg.Done(); _ = job.Close() }()
		wg.Wait()
		if err := job.Close(); err != nil {
			t.Fatalf("Close 必须幂等: %v", err)
		}
	}
}
