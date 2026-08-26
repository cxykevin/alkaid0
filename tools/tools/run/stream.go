package run

import (
	"bytes"
	"context"
	"io"

	"github.com/cxykevin/alkaid0/terminal/sandbox"
)

// runCmdWithWriter executes a non-PTY command while forwarding output chunks.
func runCmdWithWriter(ctx context.Context, c *sandbox.Command, output io.Writer, command string, usePTY bool) error {
	if usePTY {
		var buf bytes.Buffer
		err := runCmd(ctx, c, &buf, command, true)
		if buf.Len() > 0 {
			_, _ = output.Write(buf.Bytes())
		}
		return err
	}
	contextDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Kill()
		case <-contextDone:
		}
	}()
	defer close(contextDone)
	c.SetStdout(output)
	c.SetStderr(output)
	return c.Run()
}
