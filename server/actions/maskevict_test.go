package actions

import (
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	configStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// setupMaskEvictDB 打开一个独立的 :memory: 库（不复用 loadDB 的按目录缓存）。
func setupMaskEvictDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// swapMaskConfig 替换全局配置（DataMask 是 maybeEvictKeyMappings 的唯一开关来源）。
func swapMaskConfig(t *testing.T, cfg configStructs.DataMaskConfig) {
	t.Helper()
	t.Cleanup(config.GlobalConfigSwap(configStructs.Config{DataMask: cfg}))
}

// enableMaskEvict 打开“脱敏 + 淘汰”，空闲阈值取默认 24h。
func enableMaskEvict(t *testing.T) {
	t.Helper()
	swapMaskConfig(t, configStructs.DataMaskConfig{
		Enable:                true,
		KeyMappingEvictEnable: true,
		KeyMappingIdleHours:   24,
		KeyMappingMaxRows:     5000,
		KeyMappingEvictBatch:  100,
	})
}

// seedMappingRow 写一行映射；idle 为“多久之前使用过”。
func seedMappingRow(t *testing.T, db *gorm.DB, original string, idle time.Duration) {
	t.Helper()
	row := structs.KeyMapping{
		KeyType:  "apikey",
		Original: original,
		Masked:   "fake-" + original,
		LastUsed: uint64(time.Now().Add(-idle).Unix()),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create mapping %q: %v", original, err)
	}
}

func countMappings(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&structs.KeyMapping{}).Count(&n).Error; err != nil {
		t.Fatalf("count mappings: %v", err)
	}
	return n
}

// TestMaybeEvictKeyMappingsHonorsConfig 关闭总开关或淘汰开关时一行都不删。
func TestMaybeEvictKeyMappingsHonorsConfig(t *testing.T) {
	db := setupMaskEvictDB(t)
	seedMappingRow(t, db, "sk-configstale01", 100*time.Hour)

	swapMaskConfig(t, configStructs.DataMaskConfig{
		Enable:                true,
		KeyMappingEvictEnable: false,
		KeyMappingIdleHours:   24,
	})
	maybeEvictKeyMappings(t.Name(), db)
	if got := countMappings(t, db); got != 1 {
		t.Fatalf("mappings after disabled evict = %d, want 1", got)
	}

	swapMaskConfig(t, configStructs.DataMaskConfig{
		Enable:                false,
		KeyMappingEvictEnable: true,
		KeyMappingIdleHours:   24,
	})
	maybeEvictKeyMappings(t.Name(), db)
	if got := countMappings(t, db); got != 1 {
		t.Fatalf("mappings after mask disabled = %d, want 1", got)
	}
}

// TestMaybeEvictKeyMappingsDeletesStale 打开淘汰后清掉冷映射、保留热映射。
func TestMaybeEvictKeyMappingsDeletesStale(t *testing.T) {
	enableMaskEvict(t)
	db := setupMaskEvictDB(t)
	seedMappingRow(t, db, "sk-evictstale01", 100*time.Hour)
	seedMappingRow(t, db, "sk-evicthot0001", time.Minute)

	maybeEvictKeyMappings(t.Name(), db)

	var hot structs.KeyMapping
	if err := db.Where("original = ?", "sk-evicthot0001").First(&hot).Error; err != nil {
		t.Fatalf("hot mapping disappeared: %v", err)
	}
	var stale int64
	if err := db.Model(&structs.KeyMapping{}).Where("original = ?", "sk-evictstale01").Count(&stale).Error; err != nil {
		t.Fatalf("count stale: %v", err)
	}
	if stale != 0 {
		t.Fatalf("stale mapping survived: count = %d", stale)
	}
}

// TestMaybeEvictKeyMappingsThrottles 同一工作目录在间隔内只清理一次（loadSession 会被高频调用）。
func TestMaybeEvictKeyMappingsThrottles(t *testing.T) {
	enableMaskEvict(t)
	db := setupMaskEvictDB(t)

	cwd := t.Name()
	maybeEvictKeyMappings(cwd, db) // 首次调用：空表，记录清理时间
	seedMappingRow(t, db, "sk-throttled001", 100*time.Hour)

	maybeEvictKeyMappings(cwd, db)
	if got := countMappings(t, db); got != 1 {
		t.Fatalf("mappings after throttled call = %d, want 1", got)
	}

	// 换一个工作目录（另一份节流记录）就会真的清理。
	maybeEvictKeyMappings(cwd+"-other", db)
	if got := countMappings(t, db); got != 0 {
		t.Fatalf("mappings after non-throttled call = %d, want 0", got)
	}
}

// TestMaybeEvictKeyMappingsSkipsActiveStream 有活跃流式请求时不在回合中间动映射表。
func TestMaybeEvictKeyMappingsSkipsActiveStream(t *testing.T) {
	enableMaskEvict(t)
	db := setupMaskEvictDB(t)
	seedMappingRow(t, db, "sk-activestream1", 100*time.Hour)

	journal := structs.NewStreamJournal(db, 1, 256)
	if !structs.HasActiveStreams(db) {
		t.Fatal("stream journal did not register as active")
	}
	maybeEvictKeyMappings(t.Name(), db)
	if got := countMappings(t, db); got != 1 {
		t.Fatalf("mappings while streaming = %d, want 1", got)
	}

	journal.Close()
	if structs.HasActiveStreams(db) {
		t.Fatal("stream still active after Close")
	}
	// 跳过分支发生在节流之前，因此同一 cwd 立刻重试就会真的清理。
	maybeEvictKeyMappings(t.Name(), db)
	if got := countMappings(t, db); got != 0 {
		t.Fatalf("mappings after stream finished = %d, want 0", got)
	}
}

func TestKeyMappingEvictInterval(t *testing.T) {
	cases := map[int]time.Duration{
		24: 6 * time.Hour,
		48: 12 * time.Hour,
		8:  2 * time.Hour,
		6:  1 * time.Hour, // 6/4 = 1（整数除法）
		4:  1 * time.Hour,
		1:  time.Hour,
		0:  time.Hour, // 未配置的 0 也至少 1h
	}
	for hours, want := range cases {
		if got := keyMappingEvictInterval(hours); got != want {
			t.Errorf("interval(%d) = %v, want %v", hours, got, want)
		}
	}
}

func TestFormatOldestIdle(t *testing.T) {
	if got := formatOldestIdle(time.Time{}); got != "-" {
		t.Fatalf("formatOldestIdle(zero) = %q, want \"-\"", got)
	}
	got := formatOldestIdle(time.Now().Add(-90 * time.Minute))
	if !strings.HasSuffix(got, " ago") || !strings.Contains(got, "1h") {
		t.Fatalf("formatOldestIdle(90m ago) = %q, want something like \"1h30m0s ago\"", got)
	}
}
