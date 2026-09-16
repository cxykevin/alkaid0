//go:build windows

package windows

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// DrainTimeout 子进程退出后等待其输出管道排空的上限。
//
// 子进程退出后管道读端未必立刻 EOF：命令可能留下了仍持有继承句柄的后代进程
// （典型场景：powershell/cmd 启动的 npm、node、python、常驻服务）。
// 此时读端永远不会 EOF，无限等待会让执行协程卡在 Wait 上——
// 任务停不下来、kill/ESC 都不再生效。超时后强制关闭循环读写所用的管道句柄
// （internal/poll 会 CancelIoEx 取消挂起的读写）即可让搬运协程返回。
// 与 tools/tools/run 中 PTY 路径的 ptyDrainTimeout 是同一思路。
var DrainTimeout = 2 * time.Second

// JobObject 包装 Windows Job Object，把一条命令的整棵进程树作为一个整体管理。
//
// Windows 没有 Unix 的进程组语义：os.Process.Kill / TerminateProcess 只能终止
// 直接子进程。而命令实际由子进程的后代完成（powershell → npm → node），
// 只杀直接子进程会留下存活的后代进程：它们既继续占用端口/文件，也继续持有
// 继承来的 stdout/stderr 管道句柄，导致任务"kill 不掉 + 永远停在 running"。
//
// 把直接子进程加入 Job Object 后，其创建的所有后代自动属于同一个 Job，
// TerminateJobObject 即可一次性终止整棵树。
//
// 这里刻意不设置 JOB_OBJECT_LIMIT_BREAKAWAY_OK 与 KILL_ON_JOB_CLOSE：
//   - 不允许 breakaway：后代始终留在 Job 内，保证 Kill 一定能终止整棵树
//     （显式请求 breakaway 的程序会失败，这是为"杀得掉"付出的取舍）；
//   - 不在句柄关闭时终止：与 Unix 一致，命令正常结束后自行脱离的后台进程继续存活。
type JobObject struct {
	handle windows.Handle
	once   sync.Once
}

// NewJobObject 创建一个匿名 Job Object。
// 失败（极少数受限环境）时调用方应退化为只终止直接子进程。
func NewJobObject() (*JobObject, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	return &JobObject{handle: h}, nil
}

// Assign 把进程句柄加入 Job Object：此后该进程派生的所有后代自动属于同一个 Job。
// 进程已属于其它 Job 且系统不支持嵌套 Job（Windows 7 及更早）时会失败。
func (j *JobObject) Assign(process windows.Handle) error {
	if j == nil || j.handle == 0 {
		return errors.New("job object unavailable")
	}
	if process == 0 || process == windows.InvalidHandle {
		return errors.New("invalid process handle")
	}
	return windows.AssignProcessToJobObject(j.handle, process)
}

// AssignPID 打开 PID 对应进程并加入 Job Object，
// 供 os/exec 等只暴露 PID（拿不到进程句柄）的场景使用。
func (j *JobObject) AssignPID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	// AssignProcessToJobObject 要求句柄具备 PROCESS_SET_QUOTA 与 PROCESS_TERMINATE
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(h)
	return j.Assign(h)
}

// Terminate 终止 Job Object 内的全部进程（整棵进程树），幂等。
func (j *JobObject) Terminate() error {
	if j == nil || j.handle == 0 {
		return errors.New("job object unavailable")
	}
	return windows.TerminateJobObject(j.handle, 1)
}

// Close 释放 Job Object 句柄（幂等）。不主动终止其中的进程：
// 与 Unix 一致，命令正常结束后自行脱离的后台进程继续存活。
func (j *JobObject) Close() error {
	if j == nil {
		return nil
	}
	var err error
	j.once.Do(func() {
		if j.handle != 0 {
			err = windows.CloseHandle(j.handle)
			j.handle = 0
		}
	})
	return err
}
