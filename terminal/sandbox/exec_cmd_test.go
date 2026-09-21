//go:build !windows

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestExecCmdWaitDelayBoundsOrphanPipe 回归测试：主进程退出后其后代仍持有输出管道时，
// Wait 必须在 drainTimeout 内返回并视为成功，而不是一直阻塞到后代退出。
//
// 背景：stdout 接到非 *os.File writer 时 os/exec 会起拷贝 goroutine；后台进程继承
// 管道并存活时读端永不 EOF，改造前（未设置 cmd.WaitDelay）Wait 会阻塞到后代退出，
// 任务永远停在 running、kill/ESC 无效。
func TestExecCmdWaitDelayBoundsOrphanPipe(t *testing.T) {
	// 后台 sleep 继承 stdout 管道并存活 8 秒；前台 shell 立即退出。
	e := createIsolateNoneCmd(context.Background(), "sh", []string{"-c", "sleep 8 & echo hi"}, nil, "")
	var out bytes.Buffer
	e.SetStdout(&out)

	start := time.Now()
	if err := e.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	err := e.Wait()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("命令成功退出但 Wait 返回错误（exec.ErrWaitDelay 应按成功处理）: %v", err)
	}
	if elapsed > drainTimeout+2*time.Second {
		t.Fatalf("Wait 耗时 %v：超过排空上限 %v 太多，说明未设置 WaitDelay（改造前会阻塞到后代退出，约 8s）", elapsed, drainTimeout)
	}
}

// TestExecCmdKillOnWaitedProcessReturnsErrProcessDone 回归：命令进程已被 Wait 回收后
// 再 Kill 必须返回 os.ErrProcessDone，绝不能按陈旧 PID 向进程组发信号（PID 会被系统
// 复用，复用的进程若恰好是进程组组长就会被误杀）。
//
// 实现约束（CI 实测）：判定"进程已结束"必须依赖 os.Process 自身的 done 状态——
// Process.Signal 在被 Wait 回收后必然返回 ErrProcessDone；**不能读 cmd.ProcessState**，
// os/exec 的 Wait 会并发写该字段，而 Kill 常来自另一条 goroutine（run 工具的 kill 通道），
// macOS/Windows 的 -race 会直接报 data race 并让 CI 变红。
func TestExecCmdKillOnWaitedProcessReturnsErrProcessDone(t *testing.T) {
	e := createIsolateNoneCmd(context.Background(), "sh", []string{"-c", "exit 0"}, nil, "")
	if err := e.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if err := e.Wait(); err != nil {
		t.Fatalf("Wait 失败: %v", err)
	}
	if err := e.Kill(); !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("对已回收进程 Kill 应返回 os.ErrProcessDone，实际: %v", err)
	}
}

// TestExecCmdKillConcurrentWithWait 回归：Kill 与 Wait 并发（run 工具 kill 通道的真实
// 时序：doKill → job.kill → Command.Kill，同时 runCmd 正在 Wait）不得构成 data race，
// 且 Kill 必须真的终止命令。旧实现里 Kill 读 cmd.ProcessState 就是在这里被 -race 抓到的。
func TestExecCmdKillConcurrentWithWait(t *testing.T) {
	e := createIsolateNoneCmd(context.Background(), "sh", []string{"-c", "sleep 5"}, nil, "")
	if err := e.Start(); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- e.Wait() }()

	// 给 Wait 一点时间进入阻塞，再并发 Kill
	time.Sleep(200 * time.Millisecond)
	killErr := e.Kill()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Kill 后 Wait 未返回")
	}
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		t.Fatalf("Kill 返回意外错误: %v", killErr)
	}
}
