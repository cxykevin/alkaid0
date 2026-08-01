//go:build linux

package sandbox

import (
	"context"
	_ "embed" // embed
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

//go:embed scripts/linux/mount.sh
var mountScript string

// createIsolatedCommand 创建OS级隔离的命令
func (s *Sandbox) createIsolatedCommand(ctx context.Context, name string, args ...string) (*Command, error) {
	cmd, err := s.createLinuxIsolatedCommand(ctx, name, args...)
	if err != nil {
		return nil, err
	}

	return &Command{
		cmd:     CreateExecFromCmd(cmd, func() {}),
		ctx:     ctx,
		name:    name,
		args:    args,
		workDir: s.workDir,
		env:     s.env,
	}, nil
}

// createLinuxIsolatedCommand 创建Linux隔离命令
func (s *Sandbox) createLinuxIsolatedCommand(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	// 构建可写目录的 bind mount 命令（返回外部 export 与内层挂载命令）
	writableExports, writableCmds := s.generateWritableMounts()

	// 工作目录处理（确保在chroot内存在）
	chrootWorkDir := s.workDir
	if !strings.HasPrefix(chrootWorkDir, "/") {
		chrootWorkDir = "/" + chrootWorkDir
	}

	// 先chroot，再内部挂载
	// 注意：进程以 UID 0 (root) 运行，但 --map-root-user 将 UID 0
	// 映射为宿主机的真实用户，因此文件操作归属正确
	realUser := os.Getenv("USER")
	if realUser == "" {
		realUser = "user"
	}
	// 工作目录与可写目录通过环境变量传给内层脚本，
	// 避免含单引号路径拼进 sh -uc '...' 单引号字符串导致脚本语法破坏/注入
	exports := writableExports
	if exports != "" {
		exports += "\n"
	}
	exports += "export ALK_WORKDIR=" + shellQuote(chrootWorkDir)

	script := fmt.Sprintf(mountScript,
		shellQuote(realUser),
		exports,
		writableCmds,
		shellQuote(name),
	)

	// 直接通过 unshare 执行，无需临时文件
	cmd := exec.CommandContext(ctx, "unshare",
		"--user",          // 创建用户命名空间（允许非 root 创建其他命名空间）
		"--map-root-user", // 需要 root 身份才能完成 mount/chroot 等操作
		"--mount",         // 创建挂载命名空间（关键：隔离所有mount操作）
		"--pid",           // 创建PID命名空间
		"--fork",          // fork子进程作为PID 1
		"--ipc",           // IPC命名空间（可选，增强隔离）
		"--uts",           // UTS命名空间（可选，隔离hostname）
		"sh", "-c", script,
	)
	// 设置进程组，确保超时时可以杀死整个进程树
	// unshare --pid --fork 的子进程会成为孤儿进程，通过进程组 kill 可防止残留
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// 负 PID 表示向进程组发送信号
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}

	// 传递原始参数
	cmd.Args = append(cmd.Args, "--")
	cmd.Args = append(cmd.Args, args...)

	return cmd, nil
}

// generateWritableMounts 生成可写目录挂载相关命令。
// 返回两部分：外部 export 语句（shellQuote 安全）与内层挂载命令（用 "$ALK_WD_n" 引用），
// 避免含单引号路径被拼进 sh -uc '...' 单引号字符串导致语法破坏/注入。
func (s *Sandbox) generateWritableMounts() (string, string) {
	if len(s.writableDirs) == 0 {
		return "", ""
	}

	var exports []string
	var cmds []string
	for i, dir := range s.writableDirs {
		varName := fmt.Sprintf("ALK_WD_%d", i)
		exports = append(exports, fmt.Sprintf("export %s=%s", varName, shellQuote(dir)))
		// 确保目录存在，然后 rbind 并 remount rw
		cmds = append(cmds, fmt.Sprintf(`
			mkdir -p "$%s" 2>/dev/null || :
			mount --rbind "$%s" "$%s" 2>/dev/null || :
			mount -o remount,rw "$%s" 2>/dev/null || :`,
			varName, varName, varName, varName,
		))
		// 保护可写目录中的 .alkaid0 子目录（只读），防止沙箱内进程修改聊天记录和配置
		cmds = append(cmds, fmt.Sprintf(`
			if [ -d "$%s/.alkaid0" ]; then
				mount --bind "$%s/.alkaid0" "$%s/.alkaid0" 2>/dev/null || :
				mount -o remount,ro,bind "$%s/.alkaid0" 2>/dev/null || :
			fi`,
			varName, varName, varName, varName,
		))
	}
	return strings.Join(exports, "\n"), strings.Join(cmds, "\n")
}

// shellQuote 转义shell参数
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// 简单处理：单引号包裹，内部单引号转义
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
