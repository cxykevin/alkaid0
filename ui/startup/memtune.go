package startup

import (
	"os"
	"runtime/debug"
	"strings"

	"github.com/shirou/gopsutil/v4/mem"
)

// 启动时的运行时内存调优。
//
// Go 默认 GOGC=100（堆相对存活对象翻倍即触发 GC），不设 GOMEMLIMIT 时堆可以
// 一直涨到把物理内存吃满。alkaid0 是长期驻留服务，会话上下文与被注入的内容块
// 体积较大、空闲时几乎不再分配，因此启动时把内存软限制压得远低于整机内存：
// 超过水位后 GC 会更积极回收并归还内存，宁可多花一点 CPU 也要让常驻内存可控。
//
// 软限制按可用内存分档（max(128 MiB, 可用内存/32)）：
// <8 GiB → 128 MiB，8 GiB → 256 MiB，16 GiB → 512 MiB，32 GiB → 1 GiB，
// 之后每翻倍内存翻倍限制。GC 目标保持 Go 默认的 100：水位已由软限制兜住，
// 放宽 GOGC 只会放大两次 GC 之间的堆峰值。
//
// 用户的手动调优始终优先：只要设置了 GOGC / GOMEMLIMIT（或 GODEBUG 里的
// GC 相关键），启动时不再改动任何运行时参数，保证 `GOGC=off`、`GOMEMLIMIT=1GiB`
// 这类显式配置不被覆盖。
const (
	// memoryLimitFloor 是软限制下限（128 MiB）：可用内存不足 4 GiB 或
	// 探测失败时使用。
	memoryLimitFloor = 128 << 20
	// memoryScaleDenominator 是软限制占可用内存的比例分母：
	// 8 GiB 内存 → 256 MiB，16 GiB → 512 MiB，依此类推。
	memoryScaleDenominator = 32
	// gcPercent 与 Go 运行时缺省值一致；显式设置一次以免被其它代码改动。
	gcPercent = 100
)

// manualMemoryEnvVars 是 Go 运行时的手动内存调优环境变量。
var manualMemoryEnvVars = []string{"GOGC", "GOMEMLIMIT"}

// manualGCDebugKeys 是 GODEBUG 中与 GC / 内存归还相关的键：
// 出现这些键同样视为手动调优（gctrace 用于观察 GC，madvdontneed 控制
// 内存是否立即归还操作系统）。
var manualGCDebugKeys = []string{"gctrace", "gcpacertrace", "gcstoptheworld", "madvdontneed"}

// memoryTuning 是自动调优算出的运行时参数。
type memoryTuning struct {
	// GCPercent 是 debug.SetGCPercent 的目标值。
	GCPercent int
	// MemoryLimit 是 debug.SetMemoryLimit 的软限制（字节）。
	MemoryLimit int64
}

// computeMemoryTuning 根据可用内存（字节）计算调优参数。
// usable 为 0 表示探测失败或未知，此时返回下限值。
func computeMemoryTuning(usable uint64) memoryTuning {
	limit := int64(memoryLimitFloor)
	if scaled := usable / memoryScaleDenominator; scaled > uint64(limit) {
		limit = int64(scaled)
	}
	return memoryTuning{GCPercent: gcPercent, MemoryLimit: limit}
}

// manualMemoryTuningEnv 返回检测到的手动调优环境变量，未检测到时为空字符串。
func manualMemoryTuningEnv() string {
	for _, name := range manualMemoryEnvVars {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return name
		}
	}
	for item := range strings.SplitSeq(os.Getenv("GODEBUG"), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, _, _ := strings.Cut(item, "=")
		key = strings.TrimSpace(key)
		for _, gcKey := range manualGCDebugKeys {
			if key == gcKey {
				return "GODEBUG=" + gcKey
			}
		}
	}
	return ""
}

// tuneRuntimeMemory 在进程启动时按可用内存调整 GOGC 与 GOMEMLIMIT。
//
// 返回实际生效的调优参数；applied 为 false 表示检测到手动调优环境变量，
// 本次没有改动任何运行时参数。
func tuneRuntimeMemory() (tuning memoryTuning, applied bool) {
	if name := manualMemoryTuningEnv(); name != "" {
		logger.Info("runtime memory tuning skipped: %s is set manually", name)
		return memoryTuning{}, false
	}
	usable := usableMemory()
	tuning = computeMemoryTuning(usable)
	debug.SetGCPercent(tuning.GCPercent)
	debug.SetMemoryLimit(tuning.MemoryLimit)
	logger.Info("runtime memory tuned: GOGC=%d, GOMEMLIMIT=%d MiB (usable memory %d MiB)",
		tuning.GCPercent, tuning.MemoryLimit>>20, usable>>20)
	return tuning, true
}

// usableMemory 返回当前进程可用的总内存估算：物理内存与 cgroup 限额取较小值。
// 探测失败返回 0，由 computeMemoryTuning 回退到保守值。
func usableMemory() uint64 {
	physical := physicalMemory()
	limit := cgroupMemoryLimit()
	switch {
	case physical == 0:
		return limit
	case limit == 0:
		return physical
	default:
		return min(physical, limit)
	}
}

// physicalMemory 返回物理内存总量，探测失败返回 0。
func physicalMemory() uint64 {
	vm, err := mem.VirtualMemory()
	if err != nil || vm == nil {
		return 0
	}
	return vm.Total
}
