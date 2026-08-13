package stats

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// setup 重置单例并把持久化路径指向临时目录，避免污染真实配置目录。
func setup(t *testing.T) string {
	t.Helper()
	ResetForTest()
	path := filepath.Join(t.TempDir(), "usage.json")
	SetFilePath(path)
	return path
}

func TestAddUsage_SingleModel(t *testing.T) {
	setup(t)
	AddUsage(1, "Kimi K2 Thinking", 100, 50, 30)
	snap := Snapshot()
	if len(snap.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(snap.Models))
	}
	m := snap.Models[0]
	if m.ModelID != 1 || m.ModelName != "Kimi K2 Thinking" {
		t.Fatalf("unexpected model: %+v", m)
	}
	if m.PromptTokens != 100 || m.CompletionTokens != 50 || m.CachedTokens != 30 || m.Requests != 1 {
		t.Fatalf("unexpected model usage: %+v", m)
	}
	if m.CacheHitRatio != 0.3 {
		t.Fatalf("expected cache ratio 0.3, got %v", m.CacheHitRatio)
	}
	if snap.Total.PromptTokens != 100 || snap.Total.CompletionTokens != 50 || snap.Total.CachedTokens != 30 {
		t.Fatalf("unexpected total: %+v", snap.Total)
	}
	if snap.Total.Requests != 1 || snap.Total.CacheHitRatio != 0.3 {
		t.Fatalf("unexpected total meta: %+v", snap.Total)
	}
}

func TestAddUsage_MultiModel(t *testing.T) {
	setup(t)
	AddUsage(1, "Kimi", 100, 50, 30)
	AddUsage(2, "DeepSeek", 200, 100, 80)
	snap := Snapshot()
	if len(snap.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(snap.Models))
	}
	if snap.Total.PromptTokens != 300 || snap.Total.CompletionTokens != 150 || snap.Total.CachedTokens != 110 {
		t.Fatalf("total should equal sum of models: %+v", snap.Total)
	}
	if snap.Total.Requests != 2 {
		t.Fatalf("expected 2 requests, got %d", snap.Total.Requests)
	}
	// 总计缓存比 = 110/300
	if snap.Total.CacheHitRatio != math.Round(110.0/300*10000)/10000 {
		t.Fatalf("unexpected total ratio: %v", snap.Total.CacheHitRatio)
	}
}

func TestPersistence_RoundTrip(t *testing.T) {
	path := setup(t)
	AddUsage(1, "Kimi", 100, 50, 30)
	AddUsage(2, "DeepSeek", 200, 100, 80)

	// 磁盘文件确实存在且 schema 正确
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("usage file should exist: %v", err)
	}
	var fd fileData
	if err := json.Unmarshal(b, &fd); err != nil {
		t.Fatalf("usage file should be valid json: %v", err)
	}
	if fd.SchemaVersion != schemaVersion {
		t.Fatalf("expected schema version %d, got %d", schemaVersion, fd.SchemaVersion)
	}
	if len(fd.Models) != 2 {
		t.Fatalf("expected 2 model records on disk, got %d", len(fd.Models))
	}

	// 模拟重启：重置内存单例，从同一路径懒加载恢复
	ResetForTest()
	snap := Snapshot()
	if len(snap.Models) != 2 {
		t.Fatalf("expected 2 models after reload, got %d", len(snap.Models))
	}
	if snap.Total.PromptTokens != 300 || snap.Total.Requests != 2 {
		t.Fatalf("unexpected restored total: %+v", snap.Total)
	}
	if snap.Models[0].ModelID != 1 || snap.Models[1].ModelID != 2 {
		t.Fatalf("unexpected order after reload: %+v", snap.Models)
	}
}

func TestConcurrentAddUsage(t *testing.T) {
	setup(t)
	const goroutines = 8
	const perGoroutine = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				AddUsage(1, "Kimi", 10, 20, 5)
			}
		}()
	}
	wg.Wait()
	snap := Snapshot()
	total := goroutines * perGoroutine
	if snap.Total.Requests != uint64(total) {
		t.Fatalf("expected %d requests, got %d", total, snap.Total.Requests)
	}
	if snap.Total.PromptTokens != uint64(total*10) {
		t.Fatalf("expected %d prompt tokens, got %d", total*10, snap.Total.PromptTokens)
	}
	if snap.Total.CompletionTokens != uint64(total*20) {
		t.Fatalf("expected %d completion tokens, got %d", total*20, snap.Total.CompletionTokens)
	}
	if snap.Total.CachedTokens != uint64(total*5) {
		t.Fatalf("expected %d cached tokens, got %d", total*5, snap.Total.CachedTokens)
	}
}

func TestCacheHitRatio_ZeroPrompt(t *testing.T) {
	if ratio := cacheHitRatio(0, 0); ratio != 0 {
		t.Fatalf("expected 0 ratio for zero prompt, got %v", ratio)
	}
	if ratio := cacheHitRatio(0, 100); ratio != 0 {
		t.Fatalf("expected 0 ratio for zero prompt with cached, got %v", ratio)
	}
}

func TestZeroUsageSkipped(t *testing.T) {
	path := setup(t)
	AddUsage(1, "Kimi", 0, 0, 0)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("usage file should not exist after zero usage, err=%v", err)
	}
	snap := Snapshot()
	if len(snap.Models) != 0 || snap.Total.Requests != 0 {
		t.Fatalf("zero usage should be skipped entirely: %+v", snap)
	}
}

func TestModelNameOverwrite(t *testing.T) {
	setup(t)
	AddUsage(1, "Old Name", 100, 50, 30)
	AddUsage(1, "New Name", 50, 25, 15)
	snap := Snapshot()
	if len(snap.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(snap.Models))
	}
	m := snap.Models[0]
	if m.ModelName != "New Name" {
		t.Fatalf("model name should use latest value, got %q", m.ModelName)
	}
	if m.PromptTokens != 150 || m.Requests != 2 {
		t.Fatalf("usage should accumulate across renames: %+v", m)
	}
}

func TestSnapshot_Ordered(t *testing.T) {
	setup(t)
	AddUsage(3, "C", 1, 1, 1)
	AddUsage(1, "A", 1, 1, 1)
	AddUsage(2, "B", 1, 1, 1)
	snap := Snapshot()
	if len(snap.Models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(snap.Models))
	}
	for i, m := range snap.Models {
		if m.ModelID != uint32(i+1) {
			t.Fatalf("models should be sorted by ModelID ascending, got %+v", snap.Models)
		}
	}
}

func TestReset(t *testing.T) {
	setup(t)
	AddUsage(1, "Kimi", 100, 50, 30)
	AddUsage(2, "DeepSeek", 200, 100, 80)
	if err := Reset(); err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	snap := Snapshot()
	if snap.Total.Requests != 0 || snap.Total.PromptTokens != 0 ||
		snap.Total.CompletionTokens != 0 || snap.Total.CachedTokens != 0 {
		t.Fatalf("reset should clear totals: %+v", snap.Total)
	}
	if len(snap.Models) != 0 {
		t.Fatalf("reset should clear models: %+v", snap.Models)
	}
	// 模拟重启：清空状态应已持久化，重新加载仍为空
	ResetForTest()
	snap = Snapshot()
	if snap.Total.Requests != 0 || len(snap.Models) != 0 {
		t.Fatalf("reset should persist empty state: %+v", snap)
	}
}
