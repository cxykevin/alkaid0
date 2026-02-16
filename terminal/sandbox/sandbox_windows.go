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

func (s *Sandbox) createIsolatedCommand(ctx context.Context, name string, args ...string) (*Command, error) {
	if err := winSandbox.InitAlkaid0SandboxUser(); err != nil {
		return nil, fmt.Errorf("初始化沙盒用户失败: %w", err)
	}

	_, err := winSandbox.SetLimitToDir(s.writableDirs)

	release, err := winSandbox.SetLimitToWorkdir(s.workDir)
	if err != nil {
		return nil, fmt.Errorf("设置工作目录权限失败: %w", err)
	}

	// winSandbox.CreateProc()

	cmd := winSandbox.CommandContext(ctx, name, args...)
	cmd.Dir = s.workDir
	cmd.Env = s.env
	if resolved, err := exec.LookPath(name); err == nil {
		cmd.Path = resolved
	} else if cmd.Path == "" {
		cmd.Path = name
	}

	// runner := newWindowsRunner(cmd, *token, ctx)

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
