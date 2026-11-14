package mask

import (
	"fmt"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	configStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// evictTestNow 是淘汰测试固定的“当前时间”，与真实时钟完全解耦。
var evictTestNow = time.Unix(1_800_000_000, 0)

// pinEvictClock 把 evictNow 固定在 now，测试结束自动还原。
func pinEvictClock(t *testing.T, now time.Time) {
	t.Helper()
	old := evictNow
	evictNow = func() time.Time { return now }
	t.Cleanup(func() { evictNow = old })
}

// seedMapping 直接写一行映射，last_used 由调用方控制（绕过脱敏引擎的“创建即热”）。
func seedMapping(t *testing.T, db *gorm.DB, typ, original string, lastUsed uint64) uint64 {
	t.Helper()
	row := storageStructs.KeyMapping{
		KeyType:  typ,
		Original: original,
		Masked:   "fake-" + original,
		LastUsed: lastUsed,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create mapping %q: %v", original, err)
	}
	if row.LastUsed != lastUsed {
		t.Fatalf("create mapping %q: last_used = %d, want %d", original, row.LastUsed, lastUsed)
	}
	return row.ID
}

func mappingExists(t *testing.T, db *gorm.DB, id uint64) bool {
	t.Helper()
	var n int64
	if err := db.Model(&storageStructs.KeyMapping{}).Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatalf("count mapping %d: %v", id, err)
	}
	return n > 0
}

func mustEvict(t *testing.T, db *gorm.DB, policy EvictPolicy) EvictResult {
	t.Helper()
	res, err := EvictKeyMappings(db, policy)
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	return res
}

// TestEvictPolicyNormalization 锁定默认值与安全下限：阈值不得小于 prompt 缓存保留期。
func TestEvictPolicyNormalization(t *testing.T) {
	cases := []struct {
		name string
		in   EvictPolicy
		want EvictPolicy
	}{
		{"zero", EvictPolicy{}, EvictPolicy{IdleHours: 24, MaxRows: 5000, Batch: 500}},
		{"below-min", EvictPolicy{IdleHours: 1}, EvictPolicy{IdleHours: 6, MaxRows: 5000, Batch: 500}},
		{"just-below-min", EvictPolicy{IdleHours: 5}, EvictPolicy{IdleHours: 6, MaxRows: 5000, Batch: 500}},
		{"at-min", EvictPolicy{IdleHours: 6}, EvictPolicy{IdleHours: 6, MaxRows: 5000, Batch: 500}},
		{"above-min", EvictPolicy{IdleHours: 48}, EvictPolicy{IdleHours: 48, MaxRows: 5000, Batch: 500}},
		{"negative-cap", EvictPolicy{MaxRows: -1, Batch: -5}, EvictPolicy{IdleHours: 24, MaxRows: 5000, Batch: 500}},
		{"explicit", EvictPolicy{Enable: true, IdleHours: 12, MaxRows: 100, Batch: 7}, EvictPolicy{Enable: true, IdleHours: 12, MaxRows: 100, Batch: 7}},
	}
	for _, c := range cases {
		if got := c.in.normalized(); got != c.want {
			t.Errorf("%s: normalized = %+v, want %+v", c.name, got, c.want)
		}
	}
	// IdleHours 显式写 1 不能被当成“未配置”走默认值分支（那样会变成 24 而不是下限 6）。
	if got := (EvictPolicy{IdleHours: 1}).normalized().IdleHours; got != evictMinIdleHours {
		t.Fatalf("IdleHours=1 normalized to %d, want %d", got, evictMinIdleHours)
	}
}

func TestEvictPolicyFromConfig(t *testing.T) {
	restore := config.GlobalConfigSwap(configStructs.Config{DataMask: configStructs.DataMaskConfig{
		KeyMappingEvictEnable: true,
		KeyMappingIdleHours:   2,
		KeyMappingMaxRows:     123,
		KeyMappingEvictBatch:  7,
	}})
	t.Cleanup(restore)

	got := EvictPolicyFromConfig()
	want := EvictPolicy{Enable: true, IdleHours: evictMinIdleHours, MaxRows: 123, Batch: 7}
	if got != want {
		t.Fatalf("policy from config = %+v, want %+v", got, want)
	}

	restoreDefaults := config.GlobalConfigSwap(configStructs.Config{})
	t.Cleanup(restoreDefaults)
	got = EvictPolicyFromConfig()
	want = EvictPolicy{Enable: false, IdleHours: evictDefaultIdleHours, MaxRows: evictDefaultMaxRows, Batch: evictDefaultBatch}
	if got != want {
		t.Fatalf("policy from empty config = %+v, want %+v", got, want)
	}
}

// TestEvictIdleBoundary 锁定“严格小于”的边界语义，并确认 custom 永不自动删除。
func TestEvictIdleBoundary(t *testing.T) {
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)
	cut := uint64(evictTestNow.Unix()) - 24*3600

	stale := seedMapping(t, db, TypeAPIKey, "sk-stale00000001", cut-1)
	exact := seedMapping(t, db, TypeAPIKey, "sk-exact00000001", cut)
	fresh := seedMapping(t, db, TypeAPIKey, "sk-fresh00000001", cut+1)
	custom := seedMapping(t, db, TypeCustom, "custom-stale-0001", 1)

	res := mustEvict(t, db, EvictPolicy{Enable: true, IdleHours: 24, MaxRows: 5000, Batch: 100})
	if res.Deleted != 1 || res.Remaining != 3 {
		t.Fatalf("deleted/remaining = %d/%d, want 1/3", res.Deleted, res.Remaining)
	}
	if res.OverMaxRows {
		t.Fatalf("over max rows = true, want false (remaining 3 <= 5000)")
	}
	if res.IdleHours != 24 {
		t.Fatalf("idle hours = %d, want 24", res.IdleHours)
	}
	if mappingExists(t, db, stale) {
		t.Fatalf("stale mapping %d survived eviction", stale)
	}
	if !mappingExists(t, db, exact) {
		t.Fatalf("mapping exactly at the idle cut was deleted, boundary must be strict")
	}
	if !mappingExists(t, db, fresh) {
		t.Fatalf("fresh mapping %d was deleted", fresh)
	}
	if !mappingExists(t, db, custom) {
		t.Fatalf("custom mapping %d was deleted", custom)
	}
}

// TestEvictDeletesAllStaleInBatches 确认单次调用会循环清干净，且代次只递增一次
// （Engine 重建自动机是按代次惰性触发的，逐批递增会造成多次重建）。
func TestEvictDeletesAllStaleInBatches(t *testing.T) {
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)
	now := uint64(evictTestNow.Unix())

	const rows = 1200
	for i := range rows {
		seedMapping(t, db, TypeAPIKey, fmt.Sprintf("sk-batch%08d", i), now-100_000)
	}
	before := mappingVersion.Load()
	res := mustEvict(t, db, EvictPolicy{Enable: true, IdleHours: 24, MaxRows: 10_000, Batch: 500})
	if res.Deleted != rows || res.Remaining != 0 {
		t.Fatalf("deleted/remaining = %d/%d, want %d/0", res.Deleted, res.Remaining, rows)
	}
	if got := mappingVersion.Load() - before; got != 1 {
		t.Fatalf("mappingVersion bumped %d times, want exactly 1", got)
	}
}

// TestEvictKeepsHotRowsOverCap 锁定“MaxRows 只是软上限”：热窗口内的行永远不删。
func TestEvictKeepsHotRowsOverCap(t *testing.T) {
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)
	now := uint64(evictTestNow.Unix())

	var ids []uint64
	for i := range 10 {
		ids = append(ids, seedMapping(t, db, TypeAPIKey, fmt.Sprintf("sk-hot%010d", i), now-60))
	}
	before := mappingVersion.Load()
	res := mustEvict(t, db, EvictPolicy{Enable: true, IdleHours: 24, MaxRows: 5, Batch: 500})
	if res.Deleted != 0 || res.Remaining != 10 {
		t.Fatalf("deleted/remaining = %d/%d, want 0/10", res.Deleted, res.Remaining)
	}
	if !res.OverMaxRows {
		t.Fatal("over max rows = false, want true (10 remaining > cap 5)")
	}
	if got := mappingVersion.Load() - before; got != 0 {
		t.Fatalf("mappingVersion bumped %d times on a no-op eviction", got)
	}
	for _, id := range ids {
		if !mappingExists(t, db, id) {
			t.Fatalf("hot mapping %d was deleted", id)
		}
	}
}

// TestEvictDisabledOnlyReports 关闭淘汰后只统计不删除（/mask clean 的展示路径）。
func TestEvictDisabledOnlyReports(t *testing.T) {
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)
	oldest := uint64(evictTestNow.Unix()) - 100*3600

	seedMapping(t, db, TypeAPIKey, "sk-off0000000001", oldest)
	seedMapping(t, db, TypeAPIKey, "sk-off0000000002", oldest+10)

	res := mustEvict(t, db, EvictPolicy{Enable: false, IdleHours: 24, MaxRows: 1, Batch: 100})
	if res.Deleted != 0 || res.Remaining != 2 {
		t.Fatalf("deleted/remaining = %d/%d, want 0/2", res.Deleted, res.Remaining)
	}
	if !res.OverMaxRows {
		t.Fatal("over max rows = false, want true (2 remaining > cap 1)")
	}
	if !res.OldestIdle.Equal(time.Unix(int64(oldest), 0)) {
		t.Fatalf("oldest idle = %v, want %v", res.OldestIdle, time.Unix(int64(oldest), 0))
	}
}

// TestEvictForceDeletesEverythingExceptCustom 锁定 /mask clean --force 的语义。
func TestEvictForceDeletesEverythingExceptCustom(t *testing.T) {
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)
	now := uint64(evictTestNow.Unix())

	seedMapping(t, db, TypeAPIKey, "sk-force00000001", now)
	seedMapping(t, db, TypeAPIKey, "sk-force00000002", now)
	seedMapping(t, db, TypeIP, "203.0.113.77", now-10)
	custom := seedMapping(t, db, TypeCustom, "custom-keep-0001", now)

	res := mustEvict(t, db, EvictPolicy{Force: true, IdleHours: 24, MaxRows: 5000, Batch: 100})
	if res.Deleted != 3 || res.Remaining != 1 {
		t.Fatalf("deleted/remaining = %d/%d, want 3/1", res.Deleted, res.Remaining)
	}
	if res.IdleHours != 0 {
		t.Fatalf("idle hours = %d, want 0 in force mode", res.IdleHours)
	}
	if !mappingExists(t, db, custom) {
		t.Fatal("custom mapping was deleted by force eviction")
	}
}

func TestEvictIsIdempotent(t *testing.T) {
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)
	now := uint64(evictTestNow.Unix())
	seedMapping(t, db, TypeAPIKey, "sk-twice00000001", now-72*3600)

	policy := EvictPolicy{Enable: true, IdleHours: 24, MaxRows: 5000, Batch: 100}
	if res := mustEvict(t, db, policy); res.Deleted != 1 || res.Remaining != 0 {
		t.Fatalf("first evict: deleted/remaining = %d/%d, want 1/0", res.Deleted, res.Remaining)
	}
	before := mappingVersion.Load()
	if res := mustEvict(t, db, policy); res.Deleted != 0 || res.Remaining != 0 {
		t.Fatalf("second evict: deleted/remaining = %d/%d, want 0/0", res.Deleted, res.Remaining)
	}
	if got := mappingVersion.Load() - before; got != 0 {
		t.Fatalf("mappingVersion bumped %d times on an empty table", got)
	}
}

func TestEvictEmptyTable(t *testing.T) {
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)
	before := mappingVersion.Load()
	res := mustEvict(t, db, EvictPolicy{Enable: true})
	if res.Deleted != 0 || res.Remaining != 0 || res.OverMaxRows {
		t.Fatalf("empty table result = %+v", res)
	}
	if !res.OldestIdle.IsZero() {
		t.Fatalf("oldest idle = %v, want zero time", res.OldestIdle)
	}
	if got := mappingVersion.Load() - before; got != 0 {
		t.Fatalf("mappingVersion bumped %d times", got)
	}
}

// TestMaskMessagesRefreshesUsage 锁定“请求发出前刷新 last_used/hits”这条闭环：
// 淘汰只认 last_used，若刷新缺失，仍在发送窗口里的原值会被误判为冷数据。
func TestMaskMessagesRefreshesUsage(t *testing.T) {
	enableMask(t)
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)

	const original = "sk-livedemo123456"
	id := seedMapping(t, db, TypeAPIKey, original, uint64(evictTestNow.Unix())-30*3600)
	msg := []structs.Message{{Role: structs.RoleUser, Content: "please use " + original + " now"}}

	eng := NewEngine(db)
	out := eng.MaskMessages(msg)
	if out[0].Content == msg[0].Content {
		t.Fatal("content was not masked")
	}
	if out[0].Content == original {
		t.Fatal("masked content still equals the original")
	}

	var row storageStructs.KeyMapping
	if err := db.First(&row, id).Error; err != nil {
		t.Fatalf("reload mapping: %v", err)
	}
	if row.Hits != 1 {
		t.Fatalf("hits = %d, want 1", row.Hits)
	}
	if want := uint64(evictTestNow.Unix()); row.LastUsed != want {
		t.Fatalf("last_used = %d, want %d", row.LastUsed, want)
	}

	// 下一次请求（新 Engine，模拟跨请求）：命中次数累加、last_used 前移，一直保持热。
	pinEvictClock(t, evictTestNow.Add(1*time.Hour))
	eng2 := NewEngine(db)
	eng2.MaskMessages(msg)
	if err := db.First(&row, id).Error; err != nil {
		t.Fatalf("reload mapping: %v", err)
	}
	if row.Hits != 2 {
		t.Fatalf("hits = %d, want 2", row.Hits)
	}
	if want := uint64(evictTestNow.Add(1 * time.Hour).Unix()); row.LastUsed != want {
		t.Fatalf("last_used = %d, want %d", row.LastUsed, want)
	}
}

// TestEvictSparesMappingCreatedDuringRequest 刚建立的映射标记为“热”，不会在同一时刻被删。
func TestEvictSparesMappingCreatedDuringRequest(t *testing.T) {
	enableMask(t)
	pinEvictClock(t, evictTestNow)
	db := newTestDB(t)

	eng := NewEngine(db)
	eng.MaskMessages([]structs.Message{{Role: structs.RoleUser, Content: "key sk-brandnew9f8e7d6c"}})

	var row storageStructs.KeyMapping
	if err := db.Where("key_type = ?", TypeAPIKey).First(&row).Error; err != nil {
		t.Fatalf("load created mapping: %v", err)
	}
	if row.LastUsed != uint64(evictTestNow.Unix()) {
		t.Fatalf("created mapping last_used = %d, want %d", row.LastUsed, uint64(evictTestNow.Unix()))
	}
	if res := mustEvict(t, db, EvictPolicy{Enable: true, IdleHours: 24, MaxRows: 5000, Batch: 100}); res.Deleted != 0 {
		t.Fatalf("freshly created mapping deleted: %+v", res)
	}
}
