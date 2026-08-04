//go:build unix

package memory

import "syscall"

// sameDevice 判断两个路径是否位于同一文件系统。
// 兜底语义：任一 stat 失败即视为同一设备（不做文件系统边界检测，放行向上查找）。
func sameDevice(a, b string) bool {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return true
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return true
	}
	return sa.Dev == sb.Dev
}
