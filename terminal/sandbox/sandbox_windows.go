//go:build windows

package sandbox

import (
	"context"
	"fmt"
	"os/exec"

	_ "unsafe"

	winSandbox "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows"
	"golang.org/x/sys/windows"
)

//go:linkname createRunToken github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows.createRunToken
func createRunToken() (*windows.Token, error)

type windowsCommandCleanup struct {
	token   *windows.Token
	release func() error
}

func (s *Sandbox) createIsolatedCommand(ctx context.Context, name string, args ...string) (*Command, error) {
	if err := winSandbox.InitAlkaid0SandboxUser(); err != nil {
		return nil, fmt.Errorf("初始化沙盒用户失败: %w", err)
	}

	release, err := winSandbox.SetLimitToWorkdir(s.workDir)
	if err != nil {
		return nil, fmt.Errorf("设置工作目录权限失败: %w", err)
	}

	token, err := createRunToken()
	if err != nil {
		_ = release()
		return nil, fmt.Errorf("创建沙盒令牌失败: %w", err)
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = s.workDir
	cmd.Env = s.env
	if resolved, err := exec.LookPath(name); err == nil {
		cmd.Path = resolved
	} else if cmd.Path == "" {
		cmd.Path = name
	}

	runner := newWindowsRunner(cmd, *token, ctx)

	return &Command{
		cmd:     cmd,
		ctx:     ctx,
		runner:  runner,
		name:    name,
		args:    args,
		workDir: s.workDir,
		env:     s.env,
		temp:    &windowsCommandCleanup{token: token, release: release},
	}, nil
}

func (c *Command) cleanupCommand() {
	if r, ok := c.runner.(*windowsRunner); ok {
		r.Close()
	}
	if cleanup, ok := c.temp.(*windowsCommandCleanup); ok {
		if cleanup.release != nil {
			_ = cleanup.release()
		}
		if cleanup.token != nil {
			_ = cleanup.token.Close()
		}
	}
}
