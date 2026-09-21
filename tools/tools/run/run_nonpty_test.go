//go:build !windows

package run

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/terminal/sandbox"
)

// TestRunCmdNonPTYReturnsWhenDescendantHoldsPipe 回归：非 PTY 路径（type: python
// 等结构化执行）在 Unix 上不得因为"命令已退出、后代进程仍持有输出管道"而永不返回。
//
// 根因在 terminal/sandbox：runCmd 把非 *os.File 的 writer 直接交给 os/exec 时，
// exec 会为它起内部拷贝 goroutine 并在 Wait 中等待；后代进程继续持有管道写端时
// 读端永不 EOF（Windows 侧早有 WaitDelay 兜底，Unix 侧此前没有）。Unix 路径补上
// WaitDelay = drainTimeout 后，该场景必须在排空上限内返回。本用例作为 run + sandbox
// 的端到端回归：run 侧没有可独立修复的点。
func TestRunCmdNonPTYReturnsWhenDescendantHoldsPipe(t *testing.T) {
	const linger = "31415"
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "sleep "+linger).Run() })

	sb, err := sandbox.New(sandbox.Config{IsolationMode: sandbox.IsolationNone})
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}
	cmd, err := sb.Execute("sh", "-c", "sleep "+linger+" &")
	if err != nil {
		t.Fatalf("创建命令失败: %v", err)
	}

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runCmd(context.Background(), cmd, &buf, "sh -c 'sleep "+linger+" &'", false)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Kill()
		t.Fatal("命令已退出但后代仍持有输出管道时 runCmd 未返回（Unix 缺排空兜底，任务会永远停在 running）")
	}
}

// TestRunCmdNonPTYHonorsEarlyKill 回归：kill 早于 start 时非 PTY 路径不能漏杀。
//
// Job.kill() 对尚未启动的进程无效（Process 为 nil）；若 kill 恰好落在
// "注册 killFn"与"进程真正启动"之间，命令会继续跑到自然结束。PTY 路径启动后
// 会按 killRequested 补杀一次，非 PTY 路径此前没有这一步。
func TestRunCmdNonPTYHonorsEarlyKill(t *testing.T) {
	const linger = "31416"
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", "sleep "+linger).Run() })

	sb, err := sandbox.New(sandbox.Config{IsolationMode: sandbox.IsolationNone})
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}
	cmd, err := sb.Execute("sleep", linger)
	if err != nil {
		t.Fatalf("创建命令失败: %v", err)
	}

	// 模拟"kill 已经发生、进程尚未启动"：直接置位 killRequested
	job := &Job{ID: "@temp/run/early-kill", done: make(chan struct{})}
	job.killRequested = true

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runCmd(context.Background(), cmd, &buf, "sleep "+linger, false, job)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Kill()
		t.Fatal("kill 早于 start 时非 PTY 命令未被终止（漏杀，任务会跑到自然结束）")
	}
}
