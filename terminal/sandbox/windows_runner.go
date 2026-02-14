//go:build windows

package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsRunner struct {
	cmd       *exec.Cmd
	token     windows.Token
	ctx       context.Context
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	process   windows.Handle
	processID uint32
	thread    windows.Handle
}

func newWindowsRunner(cmd *exec.Cmd, token windows.Token, ctx context.Context) *windowsRunner {
	return &windowsRunner{cmd: cmd, token: token, ctx: ctx}
}

func (r *windowsRunner) SetStdin(in io.Reader)   { r.stdin = in }
func (r *windowsRunner) SetStdout(out io.Writer) { r.stdout = out }
func (r *windowsRunner) SetStderr(err io.Writer) { r.stderr = err }

func (r *windowsRunner) Start() error {
	if r.process != 0 {
		return fmt.Errorf("进程已启动")
	}

	debug := os.Getenv("ALKAID0_TEST_SANDBOX") != ""
	logf := func(format string, args ...any) {}
	if debug {
		logf = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "windows_runner: "+format+"\n", args...)
		}
	}

	cmdLine, err := windowsCommandLine(r.cmd)
	if err != nil {
		return err
	}
	cmdLinePtr, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		return fmt.Errorf("构建命令行失败: %w", err)
	}

	var cwd *uint16
	if r.cmd.Dir != "" {
		cwd, err = windows.UTF16PtrFromString(r.cmd.Dir)
		if err != nil {
			return fmt.Errorf("转换工作目录失败: %w", err)
		}
	}

	envBlockPtr, envBlock, err := buildEnvironmentBlock(r.cmd.Env)
	if err != nil {
		return fmt.Errorf("构建环境块失败: %w", err)
	}

	startup := &windows.StartupInfo{}
	startup.Cb = uint32(unsafe.Sizeof(*startup))

	// 对于 CreateProcessWithTokenW，显式设置 Desktop 有时会导致 0xC0000142 错误。
	// 除非明确需要，否则让其继承父进程桌面。
	desktopName := ""
	if os.Getenv("ALKAID0_SANDBOX_SET_DESKTOP") != "" {
		desktopName = "winsta0\\default"
		desktopPtr, err := windows.UTF16PtrFromString(desktopName)
		if err != nil {
			return fmt.Errorf("设置桌面失败: %w", err)
		}
		startup.Desktop = desktopPtr
	}

	stdinHandle, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return fmt.Errorf("获取标准输入句柄失败: %w", err)
	}
	stdoutHandle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return fmt.Errorf("获取标准输出句柄失败: %w", err)
	}
	stderrHandle, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		return fmt.Errorf("获取标准错误句柄失败: %w", err)
	}

	stdinInheritable, stdinInheritErr := handleIsInheritable(stdinHandle)
	stdoutInheritable, stdoutInheritErr := handleIsInheritable(stdoutHandle)
	stderrInheritable, stderrInheritErr := handleIsInheritable(stderrHandle)

	startup.Flags |= windows.STARTF_USESTDHANDLES
	startup.StdInput = stdinHandle
	startup.StdOutput = stdoutHandle
	startup.StdErr = stderrHandle

	var procInfo windows.ProcessInformation

	if debug {
		useStdHandles := startup.Flags&windows.STARTF_USESTDHANDLES != 0
		logf("Start cmdPath=%q args=%v dir=%q env=%d desktop=%q flags=0x%X STARTF_USESTDHANDLES=%t", r.cmd.Path, r.cmd.Args, r.cmd.Dir, len(r.cmd.Env), desktopName, startup.Flags, useStdHandles)
		logf("GetStdHandle in=0x%X invalid=%t inheritable=%t inheritErr=%v out=0x%X invalid=%t inheritable=%t inheritErr=%v err=0x%X invalid=%t inheritable=%t inheritErr=%v", uintptr(startup.StdInput), startup.StdInput == windows.InvalidHandle, stdinInheritable, stdinInheritErr, uintptr(startup.StdOutput), startup.StdOutput == windows.InvalidHandle, stdoutInheritable, stdoutInheritErr, uintptr(startup.StdErr), startup.StdErr == windows.InvalidHandle, stderrInheritable, stderrInheritErr)
		logf("CreateProcessAsUserW bInheritHandles=%d", createProcessAsUserInheritHandles)
		logf("CreateProcessWithTokenW logonFlags=0x%X", createProcessWithTokenLogonFlags())
		logf("environment block words=%d", len(envBlock))
		for _, key := range []string{"SystemRoot", "WINDIR", "ComSpec", "PATH", "PATHEXT"} {
			v, ok := lookupWindowsEnv(r.cmd.Env, key)
			if !ok {
				logf("env[%s] missing", key)
				continue
			}
			logf("env[%s] len=%d value=%q", key, len(v), truncateLogValue(v, 200))
		}
		logTokenAccessDiagnostics(r.token, r.cmd, r.cmd.Env, logf)
	}
	if err := createProcessAsUser(r.token, cmdLinePtr, cwd, envBlockPtr, startup, &procInfo); err != nil {
		if debug {
			logf("CreateProcessAsUserW failed: %v", err)
		}
		if err = createProcessWithToken(r.token, cmdLinePtr, cwd, envBlockPtr, startup, &procInfo); err != nil {
			if debug {
				logf("CreateProcessWithTokenW failed: %v", err)
			}
			return err
		}
		if debug {
			logf("CreateProcessWithTokenW ok pid=%d", procInfo.ProcessId)
		}
	} else if debug {
		logf("CreateProcessAsUserW ok pid=%d", procInfo.ProcessId)
	}
	runtime.KeepAlive(envBlock)

	r.process = procInfo.Process
	r.thread = procInfo.Thread
	r.processID = procInfo.ProcessId

	if debug {
		status, waitErr := windows.WaitForSingleObject(r.process, 150)
		if waitErr != nil {
			logf("post-create WaitForSingleObject(150ms) err=%v", waitErr)
		} else {
			switch status {
			case windows.WAIT_OBJECT_0:
				var exitCode uint32
				if err := windows.GetExitCodeProcess(r.process, &exitCode); err != nil {
					logf("post-create GetExitCodeProcess err=%v", err)
				} else {
					logf("post-create process exited quickly code=%d (0x%08X)", exitCode, exitCode)
				}
			case syscall.WAIT_TIMEOUT:
				logf("post-create process still running after 150ms")
			default:
				logf("post-create unexpected wait status=%d", status)
			}
		}
	}

	return nil
}

func (r *windowsRunner) Wait() error {
	if r.process == 0 {
		return fmt.Errorf("进程未启动")
	}
	for {
		status, err := windows.WaitForSingleObject(r.process, 200)
		if err != nil {
			return fmt.Errorf("等待进程失败: %w", err)
		}
		switch status {
		case windows.WAIT_OBJECT_0:
			var exitCode uint32
			if err := windows.GetExitCodeProcess(r.process, &exitCode); err != nil {
				return fmt.Errorf("获取退出码失败: %w", err)
			}
			if exitCode != 0 {
				if os.Getenv("ALKAID0_TEST_SANDBOX") != "" {
					fmt.Fprintf(os.Stderr, "windows_runner: process exit code=%d (0x%08X)\n", exitCode, exitCode)
				}
				return fmt.Errorf("进程退出码: %d", exitCode)
			}
			return nil
		case syscall.WAIT_TIMEOUT:
			if r.ctx != nil && r.ctx.Err() != nil {
				if err := r.Kill(); err != nil {
					return fmt.Errorf("终止超时进程失败: %w", err)
				}
				return r.ctx.Err()
			}
		default:
			return fmt.Errorf("等待进程返回未知状态: %d", status)
		}
	}
}

func (r *windowsRunner) Run() error {
	if err := r.Start(); err != nil {
		return err
	}
	return r.Wait()
}

func (r *windowsRunner) Kill() error {
	if r.process == 0 {
		return fmt.Errorf("进程未启动")
	}
	return windows.TerminateProcess(r.process, 1)
}

func (r *windowsRunner) Close() {
	if r.thread != 0 {
		windows.CloseHandle(r.thread)
		r.thread = 0
	}
	if r.process != 0 {
		windows.CloseHandle(r.process)
		r.process = 0
	}
}

func windowsCommandLine(cmd *exec.Cmd) (string, error) {
	if cmd == nil || cmd.Path == "" {
		return "", fmt.Errorf("命令路径为空")
	}

	parts := []string{syscall.EscapeArg(cmd.Path)}
	for _, arg := range cmd.Args[1:] {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " "), nil
}

const (
	logonWithProfile                  = 0x00000001
	logonNetCredentialsOnly           = 0x00000002
	createProcessAsUserInheritHandles = 1
)

func createProcessWithToken(token windows.Token, cmdLine *uint16, cwd *uint16, env *uint16, startup *windows.StartupInfo, procInfo *windows.ProcessInformation) error {
	advapi32 := windows.NewLazySystemDLL("advapi32.dll")
	procCreateProcessWithToken := advapi32.NewProc("CreateProcessWithTokenW")

	ret, _, err := procCreateProcessWithToken.Call(
		uintptr(token),
		uintptr(createProcessWithTokenLogonFlags()),
		uintptr(0),
		uintptr(unsafe.Pointer(cmdLine)),
		uintptr(windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW),
		uintptr(unsafe.Pointer(env)),
		uintptr(unsafe.Pointer(cwd)),
		uintptr(unsafe.Pointer(startup)),
		uintptr(unsafe.Pointer(procInfo)),
	)
	if ret == 0 {
		if err != nil {
			return fmt.Errorf("CreateProcessWithTokenW 失败: %w", err)
		}
		return fmt.Errorf("CreateProcessWithTokenW 失败")
	}

	return nil
}

func createProcessWithTokenLogonFlags() uint32 {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ALKAID0_SANDBOX_LOGON_MODE"))) {
	case "none", "no_profile":
		return 0
	case "netonly", "net_credentials_only":
		return logonNetCredentialsOnly
	default:
		return logonWithProfile
	}
}

func createProcessAsUser(token windows.Token, cmdLine *uint16, cwd *uint16, env *uint16, startup *windows.StartupInfo, procInfo *windows.ProcessInformation) error {
	advapi32 := windows.NewLazySystemDLL("advapi32.dll")
	procCreateProcessAsUser := advapi32.NewProc("CreateProcessAsUserW")

	ret, _, err := procCreateProcessAsUser.Call(
		uintptr(token),
		uintptr(0),
		uintptr(unsafe.Pointer(cmdLine)),
		uintptr(0),
		uintptr(0),
		uintptr(createProcessAsUserInheritHandles),
		uintptr(windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW),
		uintptr(unsafe.Pointer(env)),
		uintptr(unsafe.Pointer(cwd)),
		uintptr(unsafe.Pointer(startup)),
		uintptr(unsafe.Pointer(procInfo)),
	)
	if ret == 0 {
		if err != nil {
			return fmt.Errorf("CreateProcessAsUserW 失败: %w", err)
		}
		return fmt.Errorf("CreateProcessAsUserW 失败")
	}

	return nil
}

func buildEnvironmentBlock(env []string) (*uint16, []uint16, error) {
	if len(env) == 0 {
		return nil, nil, nil
	}

	buf := make([]uint16, 0, len(env)*16)
	for _, kv := range env {
		if strings.ContainsRune(kv, '\x00') {
			return nil, nil, fmt.Errorf("环境变量包含 NUL 字符")
		}
		u16, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, nil, err
		}
		buf = append(buf, u16...)
	}
	// 环境块需要双 NUL 结尾
	buf = append(buf, 0)

	return &buf[0], buf, nil
}

func lookupWindowsEnv(env []string, key string) (string, bool) {
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		if strings.EqualFold(kv[:i], key) {
			return kv[i+1:], true
		}
	}
	return "", false
}

func truncateLogValue(v string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(v) <= max {
		return v
	}
	if max <= 3 {
		return v[:max]
	}
	return v[:max-3] + "..."
}

func handleIsInheritable(handle windows.Handle) (bool, error) {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	procGetHandleInformation := kernel32.NewProc("GetHandleInformation")

	var flags uint32
	ret, _, callErr := procGetHandleInformation.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&flags)),
	)
	if ret == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return false, callErr
		}
		return false, syscall.EINVAL
	}

	return flags&windows.HANDLE_FLAG_INHERIT != 0, nil
}

func logTokenAccessDiagnostics(token windows.Token, cmd *exec.Cmd, env []string, logf func(string, ...any)) {
	if cmd == nil {
		logf("accessdiag skip: cmd=nil")
		return
	}

	var impToken windows.Token
	err := windows.DuplicateTokenEx(
		token,
		windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE,
		nil,
		windows.SecurityImpersonation,
		windows.TokenImpersonation,
		&impToken,
	)
	if err != nil {
		logf("accessdiag DuplicateTokenEx(TokenImpersonation) failed: %v", err)
		return
	}
	defer impToken.Close()

	systemRoot, _ := lookupWindowsEnv(env, "SystemRoot")
	comSpec, _ := lookupWindowsEnv(env, "ComSpec")

	probeCandidates := []string{
		cmd.Path,
		cmd.Dir,
		systemRoot,
		comSpec,
	}
	if systemRoot != "" {
		probeCandidates = append(probeCandidates,
			filepath.Join(systemRoot, "System32"),
			filepath.Join(systemRoot, "System32", "cmd.exe"),
		)
	}

	seen := make(map[string]struct{}, len(probeCandidates))
	probes := make([]string, 0, len(probeCandidates))
	for _, p := range probeCandidates {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		probes = append(probes, p)
	}
	if len(probes) == 0 {
		logf("accessdiag skip: no probe paths")
		return
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := impersonateLoggedOnUserForDiagnostics(impToken); err != nil {
		logf("accessdiag ImpersonateLoggedOnUser failed: %v", err)
		return
	}
	defer func() {
		if revertErr := revertToSelfForDiagnostics(); revertErr != nil {
			logf("accessdiag RevertToSelf failed: %v", revertErr)
		}
	}()

	for _, p := range probes {
		fi, statErr := os.Stat(p)
		if statErr != nil {
			logf("accessdiag stat path=%q err=%v", p, statErr)
			continue
		}

		kind := "file"
		if fi.IsDir() {
			kind = "dir"
		}
		logf("accessdiag stat path=%q type=%s mode=%s", p, kind, fi.Mode())

		f, openErr := os.Open(p)
		if openErr != nil {
			logf("accessdiag open path=%q err=%v", p, openErr)
			continue
		}

		if fi.IsDir() {
			_, readErr := f.Readdirnames(1)
			if readErr != nil && readErr != io.EOF {
				logf("accessdiag list path=%q err=%v", p, readErr)
			} else {
				logf("accessdiag list path=%q ok", p)
			}
		} else {
			buf := make([]byte, 1)
			_, readErr := f.Read(buf)
			if readErr != nil && readErr != io.EOF {
				logf("accessdiag read path=%q err=%v", p, readErr)
			} else {
				logf("accessdiag read path=%q ok", p)
			}
		}

		if closeErr := f.Close(); closeErr != nil {
			logf("accessdiag close path=%q err=%v", p, closeErr)
		}
	}
}

func impersonateLoggedOnUserForDiagnostics(token windows.Token) error {
	advapi32 := windows.NewLazySystemDLL("advapi32.dll")
	proc := advapi32.NewProc("ImpersonateLoggedOnUser")
	ret, _, callErr := proc.Call(uintptr(token))
	if ret == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return syscall.EINVAL
	}
	return nil
}

func revertToSelfForDiagnostics() error {
	advapi32 := windows.NewLazySystemDLL("advapi32.dll")
	proc := advapi32.NewProc("RevertToSelf")
	ret, _, callErr := proc.Call()
	if ret == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return syscall.EINVAL
	}
	return nil
}
