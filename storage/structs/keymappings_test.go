package structs

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupKeyMappingTest(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&KeyMapping{}); err != nil {
		t.Fatalf("迁移测试数据库失败: %v", err)
	}
	return db
}

// TestBackfillKeyMappingLastUsed 锁定升级迁移的语义：只把 last_used = 0 的历史行
// 回填为创建时间，已有 last_used 的行保持不动，且重复执行幂等。
//
// 若不回填，新增的 last_used 列默认 0，第一次自动淘汰会把全部历史映射当成
// “从远古起就没用过”而整表删掉。
func TestBackfillKeyMappingLastUsed(t *testing.T) {
	db := setupKeyMappingTest(t)

	const created = uint64(1_700_000_000)
	legacy := KeyMapping{KeyType: "apikey", Original: "orig-legacy", Masked: "fake-legacy", Time: created}
	kept := KeyMapping{KeyType: "apikey", Original: "orig-used", Masked: "fake-used", Time: created - 1000, LastUsed: created - 10}
	for _, row := range []*KeyMapping{&legacy, &kept} {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("创建映射失败: %v", err)
		}
	}
	if legacy.LastUsed != 0 {
		t.Fatalf("前置条件不成立: last_used = %d, want 0", legacy.LastUsed)
	}
	if kept.LastUsed != created-10 {
		t.Fatalf("前置条件不成立: last_used = %d, want %d", kept.LastUsed, created-10)
	}

	n, err := BackfillKeyMappingLastUsed(db)
	if err != nil {
		t.Fatalf("回填失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("回填行数 = %d, want 1", n)
	}

	var got KeyMapping
	if err := db.First(&got, legacy.ID).Error; err != nil {
		t.Fatalf("重新读取映射失败: %v", err)
	}
	if got.LastUsed != got.Time || got.LastUsed != created {
		t.Fatalf("last_used = %d, want 创建时间 %d", got.LastUsed, created)
	}
	if got.Hits != 0 {
		t.Fatalf("hits = %d, want 0（回填不应伪造命中次数）", got.Hits)
	}

	var gotKept KeyMapping
	if err := db.First(&gotKept, kept.ID).Error; err != nil {
		t.Fatalf("重新读取映射失败: %v", err)
	}
	if gotKept.LastUsed != created-10 {
		t.Fatalf("已有 last_used 的行被改写: %d, want %d", gotKept.LastUsed, created-10)
	}

	// 幂等：第二次没有 last_used = 0 的行，不应再写。
	n, err = BackfillKeyMappingLastUsed(db)
	if err != nil {
		t.Fatalf("第二次回填失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("第二次回填行数 = %d, want 0", n)
	}
}

// TestBackfillKeyMappingLastUsedWithoutTable 表还不存在时直接返回，不报错也不建表。
func TestBackfillKeyMappingLastUsedWithoutTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	n, err := BackfillKeyMappingLastUsed(db)
	if err != nil || n != 0 {
		t.Fatalf("无表时回填 = (%d, %v), want (0, nil)", n, err)
	}
	if db.Migrator().HasTable(&KeyMapping{}) {
		t.Fatal("回填不应创建 key_mappings 表")
	}
	if n, err := BackfillKeyMappingLastUsed(nil); err != nil || n != 0 {
		t.Fatalf("nil db 回填 = (%d, %v), want (0, nil)", n, err)
	}
}
