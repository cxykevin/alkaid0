//go:build windows

package lsp

import "os/exec"

// setProcessGroup Windows 没有 Unix 的进程组语义，保持默认行为
func setProcessGroup(cmd *exec.Cmd) {}

// killProcess Windows 上退化为只终止直接子进程
func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
