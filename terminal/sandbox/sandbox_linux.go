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
	// 构建可写目录的bind mount命令
	writableMounts := s.generateWritableMounts()

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
	script := fmt.Sprintf(mountScript,
		shellQuote(realUser),
		writableMounts,
		chrootWorkDir,
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

// generateWritableMounts 生成可写目录挂载命令
func (s *Sandbox) generateWritableMounts() string {
	if len(s.writableDirs) == 0 {
		return ""
	}

	var mounts []string
	for _, dir := range s.writableDirs {
		// 确保目录存在，然后rbind并remount rw
		// 使用 $T 表示chroot后的根
		mounts = append(mounts, fmt.Sprintf(`
			mkdir -p %q 2>/dev/null || :
			mount --rbind %q %q 2>/dev/null || :
			mount -o remount,rw %q 2>/dev/null || :`,
			dir, dir, dir, dir,
		))
		// 保护可写目录中的 .alkaid0 子目录（只读），防止沙箱内进程修改聊天记录和配置
		mounts = append(mounts, fmt.Sprintf(`
			if [ -d %q/.alkaid0 ]; then
				mount --bind %q/.alkaid0 %q/.alkaid0 2>/dev/null || :
				mount -o remount,ro,bind %q/.alkaid0 2>/dev/null || :
			fi`,
			dir, dir, dir, dir,
		))
	}
	return strings.Join(mounts, "\n")
}

// shellQuote 转义shell参数
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// 简单处理：单引号包裹，内部单引号转义
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
