//go:build !windows

package windows

import (
	"testing"
)

// TestCommand 测试最基础的命令执行
func TestCommand(t *testing.T) {
	t.Skip("In non-windows OS")
}
