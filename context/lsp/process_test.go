//go:build !windows

package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestStartFailureKillsProcessGroup 验证 LSP 启动失败清理时会终止整个进程组：
// 修复前只杀直接子进程，语言服务器派生的子进程会变成孤儿继续运行。
func TestStartFailureKillsProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh 不可用")
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	script := fmt.Sprintf("sleep 60 & echo $! > %s; wait", pidFile)

	cfg := LanguageServerConfig{Command: "sh", Args: []string{"-c", script}}
	client := NewClient(dir, "go", cfg)

	// initialize 不会得到响应，Start 超时后走失败清理路径
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := client.Start(ctx, cfg); err == nil {
		t.Fatal("expected Start to fail")
	}

	pid := waitForPIDFile(t, pidFile)
	defer func() {
		// 失败时清理残留的孙进程，避免污染测试环境
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}()

	if !waitProcessGone(pid, 3*time.Second) {
		t.Fatalf("孙进程 %d 仍存活：cleanupProcess 未按进程组终止", pid)
	}
}

// waitForPIDFile 轮询等待 pid 文件写入
func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("未等到孙进程 pid 文件")
	return 0
}

// waitProcessGone 轮询判断进程是否已退出
func waitProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if processGone(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// processGone 判断进程是否已退出（僵尸进程视为已退出）
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	if i := strings.LastIndexByte(string(stat), ')'); i >= 0 && i+2 < len(stat) {
		return stat[i+2] == 'Z'
	}
	return false
}
