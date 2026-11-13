package startup

import (
	"runtime/debug"
	"testing"
)

func TestComputeMemoryTuning(t *testing.T) {
	cases := []struct {
		name        string
		usable      uint64
		wantLimit   int64
		wantPercent int
	}{
		{"unknown memory falls back to the floor", 0, 128 << 20, 100},
		{"tiny memory clamps to the floor", 128 << 20, 128 << 20, 100},
		{"2GiB stays at the floor", 2 << 30, 128 << 20, 100},
		{"4GiB is exactly the floor", 4 << 30, 128 << 20, 100},
		{"8GiB scales to 256MiB", 8 << 30, 256 << 20, 100},
		{"16GiB scales to 512MiB", 16 << 30, 512 << 20, 100},
		{"32GiB scales to 1GiB", 32 << 30, 1 << 30, 100},
		{"1TiB scales to 32GiB", 1 << 40, 32 << 30, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeMemoryTuning(c.usable)
			if got.MemoryLimit != c.wantLimit {
				t.Fatalf("MemoryLimit = %d, want %d", got.MemoryLimit, c.wantLimit)
			}
			if got.GCPercent != c.wantPercent {
				t.Fatalf("GCPercent = %d, want %d", got.GCPercent, c.wantPercent)
			}
		})
	}
}

func TestManualMemoryTuningEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"none", nil, ""},
		{"GOGC", map[string]string{"GOGC": "50"}, "GOGC"},
		{"GOGC disabled", map[string]string{"GOGC": "off"}, "GOGC"},
		{"GOMEMLIMIT", map[string]string{"GOMEMLIMIT": "1GiB"}, "GOMEMLIMIT"},
		{"blank GOGC is ignored", map[string]string{"GOGC": "  "}, ""},
		{"GODEBUG gctrace", map[string]string{"GODEBUG": "gctrace=1"}, "GODEBUG=gctrace"},
		{"GODEBUG bare key", map[string]string{"GODEBUG": "gctrace"}, "GODEBUG=gctrace"},
		{"GODEBUG madvdontneed", map[string]string{"GODEBUG": "madvdontneed=0,gctrace=1"}, "GODEBUG=madvdontneed"},
		{"unrelated GODEBUG key", map[string]string{"GODEBUG": "http2debug=2"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 清掉宿主环境，避免开发机/CI 上已有的值干扰判断。
			t.Setenv("GOGC", "")
			t.Setenv("GOMEMLIMIT", "")
			t.Setenv("GODEBUG", "")
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if got := manualMemoryTuningEnv(); got != c.want {
				t.Fatalf("manualMemoryTuningEnv() = %q, want %q", got, c.want)
			}
		})
	}
}

// currentGCPercent 通过「设为 -1 再还原」读取当前 GC 目标：
// runtime/debug 没有提供 getter。
func currentGCPercent() int {
	prev := debug.SetGCPercent(-1)
	debug.SetGCPercent(prev)
	return prev
}

// currentMemoryLimit 同样用「设为 -1（不限）再还原」读取当前软限制。
func currentMemoryLimit() int64 {
	prev := debug.SetMemoryLimit(-1)
	debug.SetMemoryLimit(prev)
	return prev
}

func TestTuneRuntimeMemoryAppliesAutoTuning(t *testing.T) {
	t.Setenv("GOGC", "")
	t.Setenv("GOMEMLIMIT", "")
	t.Setenv("GODEBUG", "")

	prevPercent := debug.SetGCPercent(gcPercent)
	prevLimit := debug.SetMemoryLimit(memoryLimitFloor)
	t.Cleanup(func() {
		debug.SetGCPercent(prevPercent)
		debug.SetMemoryLimit(prevLimit)
	})

	tuning, applied := tuneRuntimeMemory()
	if !applied {
		t.Fatal("tuneRuntimeMemory() skipped tuning without manual env")
	}
	want := computeMemoryTuning(usableMemory())
	if tuning != want {
		t.Fatalf("tuning = %+v, want %+v", tuning, want)
	}
	if got := currentGCPercent(); got != want.GCPercent {
		t.Fatalf("runtime GOGC = %d, want %d", got, want.GCPercent)
	}
	if got := currentMemoryLimit(); got != want.MemoryLimit {
		t.Fatalf("runtime GOMEMLIMIT = %d, want %d", got, want.MemoryLimit)
	}
}

func TestTuneRuntimeMemorySkipsManualOverride(t *testing.T) {
	t.Setenv("GOGC", "off")
	t.Setenv("GOMEMLIMIT", "")
	t.Setenv("GODEBUG", "")

	const (
		manualPercent = 77
		manualLimit   = 123 << 20
	)
	prevPercent := debug.SetGCPercent(manualPercent)
	prevLimit := debug.SetMemoryLimit(manualLimit)
	t.Cleanup(func() {
		debug.SetGCPercent(prevPercent)
		debug.SetMemoryLimit(prevLimit)
	})

	if tuning, applied := tuneRuntimeMemory(); applied {
		t.Fatalf("tuneRuntimeMemory() applied %+v despite manual GOGC", tuning)
	}
	if got := currentGCPercent(); got != manualPercent {
		t.Fatalf("runtime GOGC = %d, want untouched %d", got, manualPercent)
	}
	if got := currentMemoryLimit(); got != manualLimit {
		t.Fatalf("runtime GOMEMLIMIT = %d, want untouched %d", got, manualLimit)
	}
}
