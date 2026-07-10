//go:build !windows

package run

import (
	"os"

	"github.com/cxykevin/alkaid0/terminal/pty"
)

// openPTYForCmd 在 Unix 上创建 PTY，返回主端和从端文件描述符
func openPTYForCmd() (master, slave *os.File, err error) {
	return pty.Open(pty.Config{
		Rows: 24,
		Cols: 80,
	})
}
