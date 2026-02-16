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
		cmd:     createExecFromCmd(cmd, func() {}),
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

	// 允许读取系统目录
	rules = append(rules, "(allow file-read* (subpath \"/usr\"))")
	rules = append(rules, "(allow file-read* (subpath \"/System\"))")
	rules = append(rules, "(allow file-read* (subpath \"/Library\"))")
	rules = append(rules, "(allow file-read* (subpath \"/\"))")

	// 允许读写指定目录
	for _, dir := range s.writableDirs {
		rules = append(rules, fmt.Sprintf("(allow file-read* file-write* (subpath \"%s\"))", dir))
	}

	return strings.Join(rules, "\n")
}
