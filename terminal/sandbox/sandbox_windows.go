//go:build windows

package sandbox

import (
	"context"
	"fmt"
	"os/exec"

	winSandbox "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows"
	"golang.org/x/sys/windows"
)

type windowsCommandCleanup struct {
	token   *windows.Token
	release func() error
}

// Clean 调用 release 还原目录权限/ACL（由 Command.Wait 统一 defer 调用）
func (cl *windowsCommandCleanup) Clean() error {
	if cl.release != nil {
		return cl.release()
	}
	return nil
}

func (s *Sandbox) createIsolatedCommand(ctx context.Context, name string, args ...string) (*Command, error) {
	if err := winSandbox.InitAlkaid0SandboxUser(); err != nil {
		return nil, fmt.Errorf("初始化沙盒用户失败: %w", err)
	}

	_, err := winSandbox.SetLimitToDir(s.writableDirs)

	release, err := winSandbox.SetLimitToWorkdir(s.workDir)
	if err != nil {
		return nil, fmt.Errorf("设置工作目录权限失败: %w", err)
	}

	cmd := winSandbox.CommandContext(ctx, name, args...)
	cmd.Dir = s.workDir
	cmd.Env = s.env
	if resolved, err := exec.LookPath(name); err == nil {
		cmd.Path = resolved
	} else if cmd.Path == "" {
		cmd.Path = name
	}

	return &Command{
		cmd:     cmd,
		ctx:     ctx,
		name:    name,
		args:    args,
		workDir: s.workDir,
		env:     s.env,
		temp:    &windowsCommandCleanup{token: nil, release: release},
	}, nil
}
