//go:build windows

package run

import (
	"errors"
	"os"
)

// openPTYForCmd 在 Windows 上不支持创建 PTY，返回错误
// Windows 使用管道对透传，不走 PTY 路径
func openPTYForCmd() (master, slave *os.File, err error) {
	return nil, nil, errors.New("PTY not supported on Windows")
}
