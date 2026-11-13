//go:build !linux

package startup

// cgroupMemoryLimit 在非 Linux 平台没有 cgroup，返回 0（只按物理内存估算）。
func cgroupMemoryLimit() uint64 { return 0 }
