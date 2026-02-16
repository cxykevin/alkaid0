package sandbox

import (
	"context"
	"io"
	"os/exec"
)

// execCmd 执行对象
type execCmd struct {
	cmd   *exec.Cmd
	clean func()
}

func createExecFromCmd(cmd *exec.Cmd, clean func()) *execCmd {
	return &execCmd{cmd: cmd, clean: clean}
}

func createIsolateNoneCmd(ctx context.Context, name string, args []string, env []string, dir string) *execCmd {

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env

	return &execCmd{cmd: cmd, clean: func() {}}
}

func (e *execCmd) Start() error {
	return e.cmd.Start()
}
func (e *execCmd) Wait() error {
	return e.cmd.Wait()
}
func (e *execCmd) Run() error {
	return e.cmd.Run()
}
func (e *execCmd) SetStdin(r io.Reader) {
	e.cmd.Stdin = r
}
func (e *execCmd) SetStdout(w io.Writer) {
	e.cmd.Stdout = w
}
func (e *execCmd) SetStderr(w io.Writer) {
	e.cmd.Stderr = w
}
func (e *execCmd) Kill() error {
	return e.cmd.Process.Kill()
}
func (e *execCmd) Clean() {
	e.clean()
	e.cmd = nil
}
