//go:build !windows

package sandbox

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"syscall"
)

// ExecCmd 执行对象
type ExecCmd struct {
	cmd   *exec.Cmd
	clean func()
}

// CreateExecFromCmd exec.Cmd 包装器
func CreateExecFromCmd(cmd *exec.Cmd, clean func()) *ExecCmd {
	return &ExecCmd{cmd: cmd, clean: clean}
}

func createIsolateNoneCmd(ctx context.Context, name string, args []string, env []string, dir string) *ExecCmd {

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// 输出 writer 不是 *os.File 时（run 工具把 stdout/stderr 接到内存 writer），
	// os/exec 会起拷贝 goroutine 逐块转发；若后代进程继承管道并存活，Wait 会
	// 永远等不到 EOF。WaitDelay 到点后 os/exec 强制关掉管道并返回
	// exec.ErrWaitDelay，由 Wait 按"命令已结束"处理。
	cmd.WaitDelay = drainTimeout
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return err
		}
		return nil
	}
	cmd.Dir = dir
	cmd.Env = env

	return &ExecCmd{cmd: cmd, clean: func() {}}
}

// Start 启动
func (e *ExecCmd) PID() int {
	if e.cmd == nil || e.cmd.Process == nil {
		return 0
	}
	return e.cmd.Process.Pid
}

func (e *ExecCmd) Start() error {
	return e.cmd.Start()
}

// Wait 等待
func (e *ExecCmd) Wait() error {
	err := e.cmd.Wait()
	// 主进程已退出、只是后代仍持有输出管道：命令本身已经结束（见 WaitDelay 注释）。
	// 若退出码为 0 则视为成功，不能因为排空兜底而误报失败（输出可能被截断）。
	if errors.Is(err, exec.ErrWaitDelay) {
		logger.Warn("output pipe still held by descendant process after exit, output may be truncated (state: %v)", e.cmd.ProcessState)
		if state := e.cmd.ProcessState; state != nil && state.Success() {
			return nil
		}
	}
	return err
}

// Run 执行
func (e *ExecCmd) Run() error {
	return e.cmd.Run()
}

// SetStdin 设置标准输入
func (e *ExecCmd) SetStdin(r io.Reader) {
	e.cmd.Stdin = r
}

// SetStdout 设置标准输出
func (e *ExecCmd) SetStdout(w io.Writer) {
	e.cmd.Stdout = w
}

// SetStderr 设置标准错误
func (e *ExecCmd) SetStderr(w io.Writer) {
	e.cmd.Stderr = w
}

// Kill 终止进程（Unix 上优先杀进程组，防止孤儿进程残留）
func (e *ExecCmd) Kill() error {
	if e.cmd == nil || e.cmd.Process == nil {
		return nil
	}
	// 直接用 Process.Signal 发送 SIGKILL：os.Process 内部有 done 标记与互斥锁，
	// 进程已退出/已被 Wait 回收时返回 os.ErrProcessDone——既能避免把信号发给
	// 复用了旧 PID 的新进程，也不会像裸 syscall.Kill 那样误杀无关进程组。
	//
	// 注意不能先读 e.cmd.ProcessState 做判断：os/exec.Cmd.Wait() 会并发写该字段，
	// 而 Kill 可能来自另一条 goroutine（run 工具的 kill 通道），-race 实测报
	// data race（macOS/Windows CI 均因此失败）。
	if err := e.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		return err
	}
	// 进程仍存活：只有它确实是独立进程组组长（Start 时设置 Setpgid，或
	// unshare --pid --fork 路径）时才杀整个进程组，清理孤儿后代。
	if pgid, err := syscall.Getpgid(e.cmd.Process.Pid); err == nil && pgid == e.cmd.Process.Pid {
		_ = syscall.Kill(-e.cmd.Process.Pid, syscall.SIGKILL)
	}
	return nil
}

// Clean 清理
func (e *ExecCmd) Clean() {
	e.clean()
	e.cmd = nil
}
