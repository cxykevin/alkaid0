//go:build windows

package memory

import "path/filepath"

// sameDevice 判断两个路径是否位于同一文件系统（Windows 上近似为同一盘符）。
// 兜底语义：任一路径无法解析盘符即视为同一设备（不做文件系统边界检测，放行向上查找）。
func sameDevice(a, b string) bool {
	va := filepath.VolumeName(a)
	vb := filepath.VolumeName(b)
	if va == "" || vb == "" {
		return true
	}
	return va == vb
}
