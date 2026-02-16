//go:build linux

package sandbox

import (
	"context"
	_ "embed" // embed
	"fmt"
	"os/exec"
	"strings"
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
		cmd:     createExecFromCmd(cmd, func() {}),
		ctx:     ctx,
		name:    name,
		args:    args,
		workDir: s.workDir,
		env:     s.env,
	}, nil
}

// createLinuxIsolatedCommand 创建Linux隔离命令（使用user namespaces）
// 零外部依赖：除 unshare/bash 外，不依赖任何预装工具
func (s *Sandbox) createLinuxIsolatedCommand(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	// 构建可写目录的bind mount命令
	writableMounts := s.generateWritableMounts()

	// 工作目录处理（确保在chroot内存在）
	chrootWorkDir := s.workDir
	if !strings.HasPrefix(chrootWorkDir, "/") {
		chrootWorkDir = "/" + chrootWorkDir
	}

	// 极简内联脚本：先chroot，再内部挂载
	// 优势：虚拟文件系统/proc/dev等只需要在内部存在，外部不污染
	script := fmt.Sprintf(mountScript,
		writableMounts,
		chrootWorkDir,
		shellQuote(name),
	)

	// 直接通过 unshare 执行，无需临时文件
	cmd := exec.CommandContext(ctx, "unshare",
		"--user",          // 创建用户命名空间
		"--map-root-user", // 当前用户映射为root (允许chroot)
		"--mount",         // 创建挂载命名空间（关键：隔离所有mount操作）
		"--pid",           // 创建PID命名空间
		"--fork",          // fork子进程作为PID 1
		"--ipc",           // IPC命名空间（可选，增强隔离）
		"--uts",           // UTS命名空间（可选，隔离hostname）
		"sh", "-c", script,
	)

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

// generateBindMounts 生成bind mount命令
func (s *Sandbox) generateBindMounts() string {
	var mounts []string
	for _, dir := range s.writableDirs {
		mounts = append(mounts, fmt.Sprintf("mount --bind %s $TMPROOT%s", dir, dir))
		mounts = append(mounts, fmt.Sprintf("mount -o remount,rw,bind $TMPROOT%s", dir))
	}
	return strings.Join(mounts, "\n")
}
