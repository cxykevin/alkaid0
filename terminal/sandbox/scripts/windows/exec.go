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
	"unsafe"

	winExtra "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows/windows_extra"
	"golang.org/x/sys/windows"
)

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
func (c *Cmd) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
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

	// argvPtr, _ := windows.UTF16PtrFromString(c.argvString())
	// var dirPtr *uint16
	// if c.Dir != "" {
	// 	dirPtr, _ = windows.UTF16PtrFromString(c.Dir)
	// }
	envPtr, _ := createEnvBlock(c.Env)

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	useStdHandles := c.Stdin != nil || c.Stdout != nil || c.Stderr != nil
	if useStdHandles {
		si.Flags = windows.STARTF_USESTDHANDLES
	}

	var err error

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

	// 3. 启动 Context 监控协程
	if c.ctx != nil {
		go func() {
			select {
			case <-c.ctx.Done():
				c.Process.Kill()
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

	c.closePipes()
	c.goroutineWait.Wait()

	// 优先返回 Context 错误
	if c.ctx != nil && c.ctx.Err() != nil {
		return c.ctx.Err()
	}

	if err == nil && !state.Success() {
		err = &exec.ExitError{ProcessState: state}
	}
	if c.goroutineErr != nil && err == nil {
		return c.goroutineErr
	}
	return err
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
}

// Kill 终止进程
func (c *Cmd) Kill() error {
	return c.Process.Kill()
}
