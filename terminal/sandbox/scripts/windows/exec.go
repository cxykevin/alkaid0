//go:build windows

package windows

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/cxykevin/alkaid0/log"
	winExtra "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows/windows_extra"
	"golang.org/x/sys/windows"
)

var logger = log.New("sandbox:windows")

type osProcess struct {
	Pid     int
	state   atomic.Uint32
	sigMu   sync.RWMutex
	handle  *processHandle
	cleanup runtime.Cleanup
}

type processHandle struct {
	handle uintptr
	refs   atomic.Int32
}

func newProcessFromHandle(pid int, handle windows.Handle) *os.Process {
	ph := &processHandle{handle: uintptr(handle)}
	ph.refs.Store(1)
	p := &osProcess{Pid: pid, handle: ph}
	return (*os.Process)(unsafe.Pointer(p))
}

// Cmd 命令
type Cmd struct {
	Path string
	Args []string
	Env  []string
	Dir  string

	EnableDirs []string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Process      *os.Process
	ProcessState *os.ProcessState

	// Context 支持
	ctx context.Context

	started  bool
	finished bool
	mu       sync.Mutex

	// 资源清理
	closeAfterStart []windows.Handle
	closeAfterWait  []io.Closer
	goroutineWait   sync.WaitGroup
	goroutineErr    error
	gMu             sync.Mutex

	// Context 取消用的同步信号
	waitDone chan struct{}

	// job 命令的整棵进程树（Job Object）。为 nil 时退化为只终止直接子进程。
	// 用 atomic.Pointer：Kill 可能来自其它 goroutine，且可能并发于 Start。
	job atomic.Pointer[winExtra.JobObject]
	// killRequested 记录"进程启动前就已被要求终止"，Start 完成后立即补杀。
	killRequested atomic.Bool
	// copyClosers 输出/输入搬运协程使用的管道句柄：排空超时后关闭它们，
	// 使挂起的 ReadFile/WriteFile 被 CancelIoEx 取消，从而结束 Wait。
	copyClosers []io.Closer
	// drainForced 是否因排空超时强制关闭了管道（此时忽略搬运协程的 IO 错误）。
	drainForced atomic.Bool
}

// Command 执行程序
func Command(name string, arg ...string) *Cmd {
	return &Cmd{
		Path: name,
		Args: append([]string{name}, arg...),
	}
}

// CommandContext 执行程序，支持 Context 取消
func CommandContext(ctx context.Context, name string, arg ...string) *Cmd {
	if ctx == nil {
		panic("nil Context")
	}
	cmd := Command(name, arg...)
	cmd.ctx = ctx
	return cmd
}

// argvString 将 Args 数组转换为 Windows 原生的命令行字符串。
func (c *Cmd) argvString() string {
	if len(c.Args) == 0 {
		return ""
	}
	// windows.ComposeCommandLine 会自动处理空格、引号和反斜杠转义
	return windows.ComposeCommandLine(c.Args)
}

// StdinPipe 设置标准输入
func (c *Cmd) StdinPipe() (io.WriteCloser, error) {
	if c.Stdin != nil {
		return nil, errors.New("exec: Stdin already set")
	}
	if c.started {
		return nil, errors.New("exec: StdinPipe after process started")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stdin = pr
	hChild, hParent := windows.Handle(pr.Fd()), windows.Handle(pw.Fd())
	windows.SetHandleInformation(hChild, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
	windows.SetHandleInformation(hParent, windows.HANDLE_FLAG_INHERIT, 0)
	c.closeAfterStart = append(c.closeAfterStart, hChild)
	return pw, nil
}

// StdoutPipe 设置标准输出
func (c *Cmd) StdoutPipe() (io.ReadCloser, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if c.started {
		return nil, errors.New("exec: StdoutPipe after process started")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stdout = pw
	hChild, hParent := windows.Handle(pw.Fd()), windows.Handle(pr.Fd())
	windows.SetHandleInformation(hChild, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
	windows.SetHandleInformation(hParent, windows.HANDLE_FLAG_INHERIT, 0)
	c.closeAfterStart = append(c.closeAfterStart, hChild)
	return pr, nil
}

// StderrPipe 设置标准错误输出
func (c *Cmd) StderrPipe() (io.ReadCloser, error) {
	if c.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	if c.started {
		return nil, errors.New("exec: StderrPipe after process started")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	c.Stderr = pw
	hChild, hParent := windows.Handle(pw.Fd()), windows.Handle(pr.Fd())
	windows.SetHandleInformation(hChild, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
	windows.SetHandleInformation(hParent, windows.HANDLE_FLAG_INHERIT, 0)
	c.closeAfterStart = append(c.closeAfterStart, hChild)
	return pr, nil
}

// handleFor 获取句柄
func (c *Cmd) handleFor(rw any, isInput bool) (windows.Handle, error) {
	if rw == nil {
		f, _ := os.OpenFile("NUL", os.O_RDWR, 0)
		h := windows.Handle(f.Fd())
		windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
		c.closeAfterStart = append(c.closeAfterStart, h)
		c.closeAfterWait = append(c.closeAfterWait, f)
		return h, nil
	}
	if f, ok := rw.(*os.File); ok {
		h := windows.Handle(f.Fd())
		windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
		return h, nil
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	var hChild, hParent windows.Handle
	if isInput {
		hChild, hParent = windows.Handle(pr.Fd()), windows.Handle(pw.Fd())
		c.closeAfterWait = append(c.closeAfterWait, pr)
		c.goroutineWait.Go(func() {
			_, err := io.Copy(pw, rw.(io.Reader))
			pw.Close()
			if err != nil {
				c.setErr(err)
			}
		})
	} else {
		hChild, hParent = windows.Handle(pw.Fd()), windows.Handle(pr.Fd())
		c.closeAfterWait = append(c.closeAfterWait, pw)
		// 记录搬运协程持有的一端：子进程退出后若其后代仍持有另一端，
		// 此处 ReadFile 会一直挂起，排空超时后需要关掉它才能结束 Wait。
		c.copyClosers = append(c.copyClosers, pr)
		c.goroutineWait.Go(func() {
			_, err := io.Copy(rw.(io.Writer), pr)
			pr.Close()
			if err != nil {
				c.setErr(err)
			}
		})
	}
	windows.SetHandleInformation(hChild, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)
	windows.SetHandleInformation(hParent, windows.HANDLE_FLAG_INHERIT, 0)
	c.closeAfterStart = append(c.closeAfterStart, hChild)
	return hChild, nil
}

// func setSecInfo(handle windows.Handle, securityInformation windows.SECURITY_INFORMATION, owner *windows.SID, group *windows.SID, dacl *windows.ACL, sacl *windows.ACL) error {
// 	objType := []windows.SE_OBJECT_TYPE{
// 		windows.SE_KERNEL_OBJECT,
// 		// windows.SE_FILE_OBJECT,
// 		// windows.SE_WINDOW_OBJECT,
// 		// windows.SE_SERVICE,
// 		// windows.SE_REGISTRY_KEY,
// 	}
// 	var ret error
// 	for _, t := range objType {
// 		ret = windows.SetSecurityInfo(handle, t, securityInformation, owner, group, dacl, sacl)
// 		if ret == nil {
// 			return nil
// 		}
// 	}
// 	return ret
// }

// func dupHandle(handle windows.Handle) (windows.Handle, error) {

// }

// Start 启动程序
func (c *Cmd) Start() (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 进程创建失败时 Start 返回错误、Wait 不会被调用，此处释放 Job Object 句柄
	defer func() {
		if err != nil {
			if job := c.job.Load(); job != nil {
				_ = job.Close()
				c.job.Store(nil)
			}
		}
	}()
	if c.started {
		return errors.New("exec: already started")
	}

	// 检查 Context 是否已经取消
	if c.ctx != nil {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}
	}

	if lp, err := exec.LookPath(c.Path); err == nil {
		c.Path = lp
	}

	c.started = true
	c.waitDone = make(chan struct{})

	// 进程树：先建 Job Object，子进程创建后立即加入，使 Kill 能终止整棵树。
	// Windows 没有进程组语义，只杀直接子进程会留下继续持有管道句柄的后代。
	if c.job.Load() == nil {
		if job, jerr := winExtra.NewJobObject(); jerr == nil {
			c.job.Store(job)
		} else {
			logger.Warn("create job object failed, fallback to killing direct child only: %v", jerr)
		}
	}

	// argvPtr, _ := windows.UTF16PtrFromString(c.argvString())
	// var dirPtr *uint16
	// if c.Dir != "" {
	// 	dirPtr, _ = windows.UTF16PtrFromString(c.Dir)
	// }
	envPtr, err := createEnvBlock(c.Env)
	if err != nil {
		// 环境块含非法字符时返回错误，避免子进程拿到残缺环境
		return err
	}

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	useStdHandles := c.Stdin != nil || c.Stdout != nil || c.Stderr != nil
	if useStdHandles {
		si.Flags = windows.STARTF_USESTDHANDLES
	}

	// err = addPrivilegeToCurrentToken("SeSecurityPrivilege")
	// if err != nil {
	// 	return err
	// }
	// err = addPrivilegeToCurrentToken("SeTakeOwnershipPrivilege")
	// if err != nil {
	// 	return err
	// }

	si.StdInput = windows.Stdin
	si.StdOutput = windows.Stdout
	si.StdErr = windows.Stderr

	handlesCollect := []windows.Handle{}
	seenHandles := map[windows.Handle]struct{}{}
	addHandle := func(h windows.Handle) {
		if h == 0 || h == windows.InvalidHandle {
			return
		}
		if _, ok := seenHandles[h]; ok {
			return
		}
		seenHandles[h] = struct{}{}
		handlesCollect = append(handlesCollect, h)
	}

	// aclObj, err := GetDACL()
	// if err != nil {
	// 	return err
	// }
	if useStdHandles {
		if si.StdInput, err = c.handleFor(c.Stdin, true); err != nil {
			return err
		}
		if c.Stdout != nil && c.Stdout == c.Stderr {
			h, err := c.handleFor(c.Stdout, false)
			if err != nil {
				return err
			}
			// h, err = winExtra.DuplicateHandleWithWriteDac(h)
			// if err != nil {
			// 	return err
			// }
			si.StdOutput, si.StdErr = h, h
			// err = setSecInfo(h, windows.DACL_SECURITY_INFORMATION, nil, nil, aclObj, nil)
			// if err != nil {
			// 	return err
			// }
			addHandle(h)
		} else {
			if si.StdOutput, err = c.handleFor(c.Stdout, false); err != nil {
				return err
			}
			// si.StdOutput, err = winExtra.DuplicateHandleWithWriteDac(si.StdOutput)
			// if err != nil {
			// 	return err
			// }
			if si.StdErr, err = c.handleFor(c.Stderr, false); err != nil {
				return err
			}
			// si.StdErr, err = winExtra.DuplicateHandleWithWriteDac(si.StdErr)
			// if err != nil {
			// 	return err
			// }
			// err = setSecInfo(si.StdOutput, windows.DACL_SECURITY_INFORMATION, nil, nil, aclObj, nil)
			// if err != nil {
			// 	return err
			// }
			// err = setSecInfo(si.StdErr, windows.DACL_SECURITY_INFORMATION, nil, nil, aclObj, nil)
			// if err != nil {
			// 	return err
			// }
			addHandle(si.StdOutput)
			addHandle(si.StdErr)
		}
		addHandle(si.StdInput)
	}

	if useStdHandles && len(handlesCollect) > 0 {
		var attrLstSz uintptr
		winExtra.LibInitializeProcThreadAttributeList(nil, 1, 0, &attrLstSz)
		attrLst := make([]byte, attrLstSz)
		err = winExtra.LibInitializeProcThreadAttributeList(
			&attrLst[0],
			1,
			0,
			&attrLstSz,
		)
		if err != nil {
			return err
		}
		defer winExtra.LibDeleteProcThreadAttributeList(&attrLst[0])

		err = winExtra.LibUpdateProcThreadAttribute(
			&attrLst[0],
			0,
			windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST,
			unsafe.Pointer(&handlesCollect[0]),
			uintptr(uintptr(len(handlesCollect))*unsafe.Sizeof(handlesCollect[0])),
			nil,
			nil,
		)
		if err != nil {
			return err
		}

		si.ProcThreadAttributeList = (*windows.ProcThreadAttributeList)(unsafe.Pointer(&attrLst[0]))
	}

	// var pi windows.ProcessInformation
	/*
		func windows.CreateProcess(
			appName *uint16,
			commandLine *uint16,
			procSecurity *windows.SecurityAttributes,
			threadSecurity *windows.SecurityAttributes,
			inheritHandles bool,
			creationFlags uint32,
			env *uint16,
			currentDir *uint16,
			startupInfo *windows.StartupInfo,
			outProcInfo *windows.ProcessInformation
		) (err error)
	*/
	if len(c.Args) == 0 {
		return errors.New("exec: no args")
	}
	argvStr := c.argvString()
	pi, err := CreateProc("", argvStr, c.Dir, &si, envPtr)
	if err != nil {
		return err
	}
	// err = windows.CreateProcess(nil, argvPtr, nil, nil, true, windows.CREATE_UNICODE_ENVIRONMENT, envPtr, dirPtr, &si, &pi)

	// 立即处理父进程中不需要的子进程句柄副本，防止泄漏
	// for _, h := range c.closeAfterStart {
	// 	windows.CloseHandle(h)
	// }
	c.closeAfterStart = nil

	// if err != nil {
	// 	// c.closePipes()
	// 	return err
	// }

	// 因此必须关闭 pi.Process 和 pi.Thread 原始句柄
	defer windows.CloseHandle(pi.Thread)

	c.Process = newProcessFromHandle(int(pi.ProcessId), pi.Process)

	// 加入 Job Object：失败（嵌套 Job 受限等）时退化为只终止直接子进程，
	// 排空超时兜底仍能保证任务不会永远停在 running。
	if job := c.job.Load(); job != nil {
		if aerr := job.Assign(pi.Process); aerr != nil {
			logger.Warn("assign process %d to job object failed, fallback to killing direct child only: %v", pi.ProcessId, aerr)
			_ = job.Close()
			c.job.Store(nil)
		}
	}
	// 进程启动前就已被要求终止：此刻补杀，关掉 kill 早于 start 的竞态窗口
	if c.killRequested.Load() {
		_ = c.Kill()
	}

	// 3. 启动 Context 监控协程
	if c.ctx != nil {
		go func() {
			select {
			case <-c.ctx.Done():
				// 终止整棵进程树（只杀直接子进程会留下继承管道句柄的后代）
				_ = c.Kill()
			case <-c.waitDone:
				// 进程正常退出，结束监控协程
			}
		}()
	}

	return nil
}

// Wait 等待程序结束
func (c *Cmd) Wait() error {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return errors.New("exec: not started")
	}
	if c.finished {
		c.mu.Unlock()
		return errors.New("exec: Wait was already called")
	}
	c.finished = true
	c.mu.Unlock()

	// 等待进程结束
	state, err := c.Process.Wait()
	c.ProcessState = state

	// 通知 Context 监控协程退出，防止泄漏
	if c.waitDone != nil {
		close(c.waitDone)
	}

	// 关掉父进程持有的写端副本：子进程退出且没有别的进程持有写端时，
	// 读端立刻 EOF，搬运协程瞬时结束（正常路径）。
	c.closePipes()

	// 排空输出：命令可能留下仍持有写端句柄的后代进程（powershell 启动的
	// node/npm、常驻服务等），此时 EOF 永远不会到来。无限等待会让整条命令
	// 永远停在 running 且 kill/ESC 都不再生效，因此用上限兜底：超时后关闭
	// 搬运协程持有的管道句柄（internal/poll 会 CancelIoEx 取消挂起的读写），
	// 让协程返回，与 os/exec 的 WaitDelay / PTY 路径的 ptyDrainTimeout 同理。
	drained := make(chan struct{})
	go func() {
		c.goroutineWait.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(winExtra.DrainTimeout):
		c.drainForced.Store(true)
		logger.Warn("pipe not drained within %s, force close copy handles (command: %s)", winExtra.DrainTimeout, c.Path)
		c.forceCloseCopyHandles()
		c.goroutineWait.Wait()
	}

	// 命令已结束：释放 Job Object 句柄（不终止其中进程，与 Unix 语义一致，
	// 命令自行脱离的后台进程继续存活）。
	if job := c.job.Load(); job != nil {
		_ = job.Close()
	}

	// 优先返回 Context 错误
	if c.ctx != nil && c.ctx.Err() != nil {
		return c.ctx.Err()
	}

	if err == nil && !state.Success() {
		err = &exec.ExitError{ProcessState: state}
	}
	// 强制关闭管道造成的 IO 错误是排空兜底的副产物，不代表命令失败。
	if c.goroutineErr != nil && err == nil && !c.drainForced.Load() {
		return c.goroutineErr
	}
	return err
}

// forceCloseCopyHandles 关闭搬运协程持有的管道句柄，解除其挂起的读写。
// os.File.Close 在 Windows 上会对管道句柄调用 CancelIoEx，
// 因此阻塞中的 ReadFile/WriteFile 会立刻返回，协程得以退出。
func (c *Cmd) forceCloseCopyHandles() {
	c.mu.Lock()
	closers := c.copyClosers
	c.copyClosers = nil
	c.mu.Unlock()
	for _, cl := range closers {
		_ = cl.Close()
	}
}

// Run 启动程序并等待结束
func (c *Cmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

// Output 启动程序并等待结束，返回标准输出
func (c *Cmd) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout bytes.Buffer
	c.Stdout = &stdout
	captureStderr := false
	if c.Stderr == nil {
		c.Stderr = &bytes.Buffer{}
		captureStderr = true
	}
	err := c.Run()
	if err != nil && captureStderr {
		if ee, ok := err.(*exec.ExitError); ok {
			ee.Stderr = c.Stderr.(*bytes.Buffer).Bytes()
		}
	}
	return stdout.Bytes(), err
}

// CombinedOutput 启动程序并等待结束，返回标准输出和标准错误
func (c *Cmd) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil || c.Stderr != nil {
		return nil, errors.New("exec: Stdout/Stderr already set")
	}
	var b bytes.Buffer
	c.Stdout, c.Stderr = &b, &b
	err := c.Run()
	return b.Bytes(), err
}

// closePipes 关闭管道
func (c *Cmd) closePipes() {
	for _, f := range c.closeAfterWait {
		f.Close()
	}
	c.closeAfterWait = nil
}

// setErr 设置错误
func (c *Cmd) setErr(err error) {
	c.gMu.Lock()
	defer c.gMu.Unlock()
	if c.goroutineErr == nil {
		c.goroutineErr = err
	}
}

// createEnvBlock 创建环境变量块
func createEnvBlock(env []string) (*uint16, error) {
	if env == nil {
		return nil, nil
	}
	if len(env) == 0 {
		b := []uint16{0, 0}
		return &b[0], nil
	}
	var res []uint16
	for _, e := range env {
		u, err := windows.UTF16FromString(e)
		if err != nil {
			return nil, err
		}
		res = append(res, u...)
	}
	res = append(res, 0)
	return &res[0], nil
}

// SetStdin 设置标准输入
func (c *Cmd) SetStdin(r io.Reader) {
	c.Stdin = r
}

// SetStdout 设置标准输出
func (c *Cmd) SetStdout(w io.Writer) {
	c.Stdout = w
}

// SetStderr 设置标准错误
func (c *Cmd) SetStderr(w io.Writer) {
	c.Stderr = w
}

// Clean 清理
func (c *Cmd) Clean() {
	if job := c.job.Load(); job != nil {
		_ = job.Close()
	}
}

// Kill 终止命令：优先终止 Job Object 内的整棵进程树，
// 无法使用 Job Object 时退化为只终止直接子进程。
//
// 直接子进程通常是 powershell/cmd，真正干活的是其后代（npm/node/python）：
// 只杀直接子进程既杀不掉实际进程，也让后代继续持有继承来的 stdout/stderr
// 句柄，任务会一直卡在 running。
func (c *Cmd) Kill() error {
	// 进程可能尚未启动（Kill 早于 Start）：记录请求，Start 完成后立即补杀
	c.killRequested.Store(true)
	if job := c.job.Load(); job != nil {
		if terr := job.Terminate(); terr == nil {
			return nil
		}
	}
	if c.Process == nil {
		return errors.New("exec: process not started")
	}
	return c.Process.Kill()
}
