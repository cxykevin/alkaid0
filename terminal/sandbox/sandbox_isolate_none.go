package sandbox

import (
	"context"
	"io"
	"os/exec"
)

// ExecCmd 执行对象
type ExecCmd struct {
	cmd   *exec.Cmd
	clean func()
}

// CreateExecFromCmd exec.Cmd 包装器
func CreateExecFromCmd(cmd *exec.Cmd, clean func()) *ExecCmd {
	return &ExecCmd{cmd: cmd, clean: clean}
}

func createIsolateNoneCmd(ctx context.Context, name string, args []string, env []string, dir string) *ExecCmd {

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env

	return &ExecCmd{cmd: cmd, clean: func() {}}
}

// Start 启动
func (e *ExecCmd) Start() error {
	return e.cmd.Start()
}

// Wait 等待
func (e *ExecCmd) Wait() error {
	return e.cmd.Wait()
}

// Run 执行
func (e *ExecCmd) Run() error {
	return e.cmd.Run()
}

// SetStdin 设置标准输入
func (e *ExecCmd) SetStdin(r io.Reader) {
	e.cmd.Stdin = r
}

// SetStdout 设置标准输出
func (e *ExecCmd) SetStdout(w io.Writer) {
	e.cmd.Stdout = w
}

// SetStderr 设置标准错误
func (e *ExecCmd) SetStderr(w io.Writer) {
	e.cmd.Stderr = w
}

// Kill 终止
func (e *ExecCmd) Kill() error {
	return e.cmd.Process.Kill()
}

// Clean 清理
func (e *ExecCmd) Clean() {
	e.clean()
	e.cmd = nil
}
