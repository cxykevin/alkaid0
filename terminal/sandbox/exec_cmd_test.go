//go:build !windows

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
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

// TestExecCmdKillDoesNotSignalStalePID 回归测试：命令进程已被 Wait 回收后再调用 Kill，
// 不得按陈旧 PID 杀进程组——PID 会被系统复用，复用的进程若恰好是进程组组长就会被误杀。
//
// 构造：decoy 模拟"复用了旧 PID 的无关进程"（独立进程组组长）；ExecCmd 的
// ProcessState 非空表示该命令已结束（真实场景中由 Wait 写入）。
// 旧实现会直接 syscall.Kill(-pid) 杀掉 decoy；新实现必须不向该 PID 发信号。
func TestExecCmdKillDoesNotSignalStalePID(t *testing.T) {
	decoy := exec.Command("sleep", "30")
	decoy.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := decoy.Start(); err != nil {
		t.Fatalf("启动 decoy 失败: %v", err)
	}
	decoyPid := decoy.Process.Pid
	defer func() {
		_ = syscall.Kill(-decoyPid, syscall.SIGKILL)
		_, _ = decoy.Process.Wait()
	}()

	e := &ExecCmd{cmd: &exec.Cmd{Process: decoy.Process, ProcessState: &os.ProcessState{}}}
	err := e.Kill()
	if !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("对已结束命令 Kill 应返回 os.ErrProcessDone，实际: %v", err)
	}

	// decoy 必须仍然存活。用 wait4(WNOHANG) 判断：被杀的子进程此时已是僵尸，
	// 单看 kill(pid, 0) 会误判为存活。
	var status syscall.WaitStatus
	wpid, werr := syscall.Wait4(decoyPid, &status, syscall.WNOHANG, nil)
	if werr != nil || wpid != 0 {
		t.Fatalf("无关进程（模拟被复用的 PID %d）被误杀: wait4 pid=%d err=%v", decoyPid, wpid, werr)
	}
}
