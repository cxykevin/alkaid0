//go:build !windows

package sandbox

// cleanupCommand 清理命令资源（非 Windows 平台空实现）
func (c *Command) cleanupCommand() {
	// 非 Windows 平台不需要额外清理
}
