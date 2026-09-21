//go:build !windows

package lsp

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 让语言服务器成为独立进程组的组长，
// 使其派生的子进程与 alkaid0 自身进程组隔离，便于整体终止
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcess 终止语言服务器进程
// Unix 上优先杀整个进程组，避免 gopls/jdtls 等派生的子进程成为孤儿继续运行
func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// 仅当进程确实是独立进程组的组长时才杀进程组，避免误杀自身进程组
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil && pgid == cmd.Process.Pid {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
	}
	return cmd.Process.Kill()
}
