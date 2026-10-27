//go:build darwin

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// createIsolatedCommand 创建OS级隔离的命令
func (s *Sandbox) createIsolatedCommand(ctx context.Context, name string, args ...string) (*Command, error) {
	cmd, err := s.createDarwinIsolatedCommand(ctx, name, args...)
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

// createDarwinIsolatedCommand 创建macOS隔离命令（使用sandbox-exec）
func (s *Sandbox) createDarwinIsolatedCommand(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	// 生成Seatbelt配置
	profile := s.generateSeatbeltProfile()

	// 创建临时配置文件
	tmpFile, err := os.CreateTemp("", "sandbox-*.sb")
	if err != nil {
		return nil, fmt.Errorf("创建临时配置文件失败: %w", err)
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(profile); err != nil {
		return nil, fmt.Errorf("写入配置文件失败: %w", err)
	}

	// 使用sandbox-exec执行
	fullArgs := append([]string{"-f", tmpFile.Name(), name}, args...)
	cmd := exec.CommandContext(ctx, "sandbox-exec", fullArgs...)

	// 输出 writer 不是 *os.File 时会起拷贝 goroutine；沙盒命令留下持有管道的
	// 后代进程时 Wait 会永久阻塞（任务停在 running、kill/ESC 失效）。
	// WaitDelay 到点后 os/exec 强制关管道并返回 exec.ErrWaitDelay（见 ExecCmd.Wait）。
	cmd.WaitDelay = drainTimeout

	// 必须显式设置工作目录与环境：exec.Cmd 默认继承 daemon 进程的 cwd 与完整环境
	// （其中含 Alkaid0 自身的 API key 等敏感变量），命令会在错误的目录、带着不该有的
	// 环境运行。Windows 路径已设置 cmd.Dir/cmd.Env，macOS 此前漏了。
	cmd.Dir = s.workDir
	cmd.Env = s.env

	return cmd, nil
}

// generateSeatbeltProfile 生成Seatbelt配置
func (s *Sandbox) generateSeatbeltProfile() string {
	var rules []string

	// 基本规则：拒绝所有
	rules = append(rules, "(version 1)")
	rules = append(rules, "(deny default)")

	// 允许基本操作
	rules = append(rules, "(allow process-exec*)")
	rules = append(rules, "(allow process-fork)")
	rules = append(rules, "(allow sysctl-read)")

	// 允许读取系统目录（全盘默认只读，与 Linux/Windows 的策略保持一致）
	rules = append(rules, "(allow file-read* (subpath \"/usr\"))")
	rules = append(rules, "(allow file-read* (subpath \"/System\"))")
	rules = append(rules, "(allow file-read* (subpath \"/Library\"))")
	rules = append(rules, "(allow file-read* (subpath \"/\"))")

	// 路径策略：seatbelt 按规则顺序求值，最后匹配的规则生效，
	// 因此"拒绝"必须写在"放开"之前（配置目录里的 venv 要在拒绝之后再放开）。
	var readWrite, readOnly, deny []string
	if s.policy != nil {
		readWrite = s.policy.Paths(AccessReadWrite)
		readOnly = s.policy.Paths(AccessReadOnly)
		deny = s.policy.Paths(AccessDeny)
	} else {
		readWrite = s.writableDirs
	}
	for _, dir := range deny {
		rules = append(rules, fmt.Sprintf("(deny file-read* file-write* (subpath %q))", dir))
	}
	for _, dir := range readWrite {
		rules = append(rules, fmt.Sprintf("(allow file-read* file-write* (subpath %q))", dir))
	}
	for _, dir := range readOnly {
		rules = append(rules, fmt.Sprintf("(allow file-read* (subpath %q))", dir))
	}

	return strings.Join(rules, "\n")
}
