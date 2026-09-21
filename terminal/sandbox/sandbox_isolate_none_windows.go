//go:build windows

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync/atomic"
	"syscall"
	"unsafe"

	winExtra "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows/windows_extra"
	"golang.org/x/sys/windows"
)

// ExecCmd 执行对象
type ExecCmd struct {
	cmd   *exec.Cmd
	clean func()
	// job 命令的整棵进程树（Job Object）；为 nil 时退化为只终止直接子进程。
	// 用 atomic.Pointer：Kill 可能来自其它 goroutine，且可能并发于 Start/Wait。
	job atomic.Pointer[winExtra.JobObject]
}

// CreateExecFromCmd exec.Cmd 包装器
func CreateExecFromCmd(cmd *exec.Cmd, clean func()) *ExecCmd {
	return &ExecCmd{cmd: cmd, clean: clean}
}

func createIsolateNoneCmd(ctx context.Context, name string, args []string, env []string, dir string) *ExecCmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	// 子进程退出后管道未必立刻 EOF：命令可能留下仍持有继承句柄的后代进程
	// （典型：powershell/cmd 启动的 node、npm、常驻服务）。此时 Wait 会无限
	// 阻塞，任务永远停在 running、kill/ESC 都不再生效。WaitDelay 让 Wait 在
	// 超时后关闭管道并返回（ErrWaitDelay 在 Wait 中按"命令已结束"处理）。
	cmd.WaitDelay = drainTimeout
	// 以挂起方式创建进程：Start 里先把 PID 加入 Job Object 再恢复运行，
	// 关闭"进程已开始运行、但尚未加入 Job"这段窗口内派生的孙进程逃出
	// Job 树、导致 Kill 杀不干净的竞态（见 resumeSuspendedProcess）。
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}

	e := &ExecCmd{cmd: cmd, clean: func() {}}
	// Windows 没有进程组语义，只杀直接子进程会留下存活且继续持有管道的后代，
	// 因此用 Job Object 管理整棵进程树（见 windows_extra.JobObject）。
	if job, err := winExtra.NewJobObject(); err == nil {
		e.job.Store(job)
		// exec.CommandContext 默认的 Cancel 只 kill 直接子进程，改为终止整棵树
		cmd.Cancel = func() error { return e.Kill() }
	} else {
		logger.Warn("create job object failed, fallback to killing direct child only: %v", err)
	}

	return e
}

// PID returns the child process ID when available.
func (e *ExecCmd) PID() int {
	if e.cmd == nil || e.cmd.Process == nil {
		return 0
	}
	return e.cmd.Process.Pid
}

// Start 启动
func (e *ExecCmd) Start() error {
	if err := e.cmd.Start(); err != nil {
		// 进程未创建成功：释放 Job Object 句柄（Wait 不会被调用）
		if job := e.job.Load(); job != nil {
			_ = job.Close()
			e.job.Store(nil)
		}
		return err
	}
	// 进程创建后立即加入 Job Object：此后它派生的所有后代都属于同一个 Job，
	// Kill 时可一次性终止整棵树。加入失败（嵌套 Job 受限等）时退化为只杀直接子进程，
	// 此时仍有 WaitDelay 兜底保证任务不会永远停在 running。
	if job := e.job.Load(); job != nil && e.cmd.Process != nil {
		if err := job.AssignPID(e.cmd.Process.Pid); err != nil {
			logger.Warn("assign pid %d to job object failed, fallback to killing direct child only: %v", e.cmd.Process.Pid, err)
			_ = job.Close()
			e.job.Store(nil)
		}
	}
	// 进程此前一直挂起（CREATE_SUSPENDED），必须在加入 Job 成功后恢复运行。
	// 恢复失败则终止进程并返回错误，避免留下永远挂起、Wait 永不返回的进程。
	if e.cmd.Process != nil {
		if err := resumeSuspendedProcess(e.cmd.Process.Pid); err != nil {
			_ = e.Kill()
			return fmt.Errorf("恢复挂起进程 %d 失败: %w", e.cmd.Process.Pid, err)
		}
	}
	return nil
}

// resumeSuspendedProcess 恢复以 CREATE_SUSPENDED 创建的进程的主线程。
// 刚创建且尚未运行的进程只有一个线程，按 OwnerProcessID 枚举即可找到它。
func resumeSuspendedProcess(pid int) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	err = windows.Thread32First(snapshot, &entry)
	for err == nil {
		if entry.OwnerProcessID == uint32(pid) {
			thread, oerr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if oerr != nil {
				return fmt.Errorf("OpenThread(%d): %w", entry.ThreadID, oerr)
			}
			_, rerr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			if rerr != nil {
				return fmt.Errorf("ResumeThread(%d): %w", entry.ThreadID, rerr)
			}
			return nil
		}
		err = windows.Thread32Next(snapshot, &entry)
	}
	return fmt.Errorf("未找到 pid %d 的主线程: %w", pid, err)
}

// Wait 等待
func (e *ExecCmd) Wait() error {
	err := e.cmd.Wait()
	if job := e.job.Load(); job != nil {
		_ = job.Close()
	}
	// 子进程已退出，只是其后代仍持有输出管道：命令本身已经结束了，
	// 若退出码为 0 则视为成功（输出可能被截断，不能因为排空兜底而误报失败）。
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
	if err := e.Start(); err != nil {
		return err
	}
	return e.Wait()
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

// Kill 终止进程：优先终止 Job Object 内的整棵进程树，
// 无 Job Object 时退化为只终止直接子进程。
func (e *ExecCmd) Kill() error {
	if e.cmd == nil {
		return nil
	}
	// 先终止 Job：即使进程尚未 Start（Process 为 nil），Start 后加入 Job 的
	// 进程也会随即被终止，避免"kill 早于 start"漏杀。
	if job := e.job.Load(); job != nil {
		if err := job.Terminate(); err == nil {
			return nil
		}
	}
	if e.cmd.Process == nil {
		return nil
	}
	return e.cmd.Process.Kill()
}

// Clean 清理
func (e *ExecCmd) Clean() {
	if job := e.job.Load(); job != nil {
		_ = job.Close()
	}
	e.clean()
	e.cmd = nil
}
