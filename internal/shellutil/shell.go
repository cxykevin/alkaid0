// Package shellutil 统一 shell 选择与「用 shell 执行一条命令」的参数约定。
//
// run 工具与事件钩子（Hooks）共用这套规则，保证两处默认 shell 与执行方式一致：
// 配置 Agent.UseShell 非空时直接使用，为空时按平台默认
// （linux=bash、darwin=zsh、windows=powershell.exe、其它=bash）。
package shellutil

import "runtime"

// Default 返回实际使用的 shell 程序名。
func Default(configured string) string {
	if configured != "" {
		return configured
	}
	switch runtime.GOOS {
	case "linux":
		return "bash"
	case "darwin":
		return "zsh"
	case "windows":
		return "powershell.exe"
	default:
		return "bash"
	}
}

// Args 返回用 shell 执行 command 时的参数列表（不含 shell 程序本身）。
// powershell 系列用 -NoProfile -Command（跳过用户的 profile 脚本，避免启动慢与
// 个人配置干扰），cmd 用 /C，其余按 POSIX shell 的 -c 处理。
func Args(shell, command string) []string {
	switch shell {
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		return []string{"-NoProfile", "-Command", command}
	case "cmd", "cmd.exe":
		return []string{"/C", command}
	default:
		return []string{"-c", command}
	}
}
