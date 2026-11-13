//go:build linux

package startup

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroupRootPath 是 cgroup 文件系统的挂载点。
const cgroupRootPath = "/sys/fs/cgroup"

// cgroupMemoryTarget 描述进程所在 cgroup 目录与内存限额文件名。
type cgroupMemoryTarget struct {
	// dir 是相对于 cgroupRootPath 的目录。
	dir string
	// file 是限额文件：cgroup v2 为 memory.max，v1 为 memory/memory.limit_in_bytes。
	file string
}

// parseCgroupMemoryTargets 从 /proc/self/cgroup 的内容解析内存 cgroup 目标。
// cgroup v2 只有一行 "0::/path"，v1 则是 "N:controller,...:/path"。
func parseCgroupMemoryTargets(content string) []cgroupMemoryTarget {
	var targets []cgroupMemoryTarget
	for line := range strings.SplitSeq(content, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) != 3 {
			continue
		}
		dir := fields[2]
		if dir == "" {
			dir = "/"
		}
		switch {
		case fields[0] == "0" && fields[1] == "":
			targets = append(targets, cgroupMemoryTarget{dir: dir, file: "memory.max"})
		case strings.Contains(","+fields[1]+",", ",memory,"):
			targets = append(targets, cgroupMemoryTarget{dir: dir, file: "memory/memory.limit_in_bytes"})
		}
	}
	return targets
}

// cgroupMemoryLimit 返回进程所在 cgroup 的内存限额；没有限制或读取失败时返回 0。
func cgroupMemoryLimit() uint64 {
	data, _ := os.ReadFile("/proc/self/cgroup")
	return cgroupMemoryLimitUnder(cgroupRootPath, string(data))
}

// cgroupMemoryLimitUnder 在给定的 cgroup 挂载点下解析限额：从进程所在目录
// 逐级向上读取，取所有层级里最小的有效限额（服务管理器/容器可以在任意层级
// 设置限制，只看当前目录会漏掉上级的 MemoryMax 等约束）。
func cgroupMemoryLimitUnder(root, cgroupContent string) uint64 {
	targets := parseCgroupMemoryTargets(cgroupContent)
	if len(targets) == 0 {
		// /proc/self/cgroup 不可读时退回挂载点根目录：容器内 cgroup
		// 命名空间的根目录通常就是该容器的层级。
		targets = []cgroupMemoryTarget{
			{dir: "/", file: "memory.max"},
			{dir: "/", file: "memory/memory.limit_in_bytes"},
		}
	}
	var limit uint64
	for _, target := range targets {
		for dir := target.dir; ; {
			if n, ok := readMemoryLimitFile(filepath.Join(root, dir, target.file)); ok && (limit == 0 || n < limit) {
				limit = n
			}
			parent := filepath.Dir(dir)
			if dir == "/" || parent == dir {
				break
			}
			dir = parent
		}
	}
	return limit
}

// readMemoryLimitFile 读取一个 cgroup 内存限额文件。空内容、"max"、0 以及
// cgroup v1 用来表示"不限制"的超大哨兵值都视为未设置。
func readMemoryLimitFile(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value := strings.TrimSpace(string(data))
	if value == "" || value == "max" {
		return 0, false
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n == 0 || n >= 1<<62 {
		return 0, false
	}
	return n, true
}
