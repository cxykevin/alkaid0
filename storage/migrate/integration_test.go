package migrate_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/migrate"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openRaw(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db %s: %v", path, err)
	}
	return db
}

func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}
}

// readNullable 读取一个可空整型列，NULL 返回 nil。
func readNullable(t *testing.T, db *gorm.DB, query string, args ...any) *uint64 {
	t.Helper()
	var v sql.NullInt64
	row := db.Raw(query, args...).Row()
	if row == nil {
		t.Fatalf("query %q returned no row handle", query)
	}
	if err := row.Scan(&v); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if !v.Valid {
		return nil
	}
	u := uint64(v.Int64)
	return &u
}

func countInt(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func assertNullable(t *testing.T, what string, got, want *uint64) {
	t.Helper()
	equal := (got == nil && want == nil) || (got != nil && want != nil && *got == *want)
	if !equal {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// TestInitStorageMigratesLegacyDatabase 集成测试：应用启动路径（InitStorage）打开
// 历史库时自动完成树形迁移；再次打开（metadata 表已存在）时跳过、不重复回填。
func TestInitStorageMigratesLegacyDatabase(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".alkaid0")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbFile := filepath.Join(dataDir, "db.sqlite")

	// 构造 v0.5.8 结构的旧库
	legacy := openRaw(t, dbFile)
	migrate.ApplyLegacyFixtureForTest(t, legacy)
	closeDB(t, legacy)

	// 第一次打开：应完成迁移
	db, err := storage.InitStorage(dataDir, "db.sqlite")
	if err != nil {
		t.Fatalf("first InitStorage: %v", err)
	}
	if n := countInt(t, db, "SELECT count(*) FROM pragma_table_info('messages') WHERE name = 'parent_id'"); n != 1 {
		t.Fatal("messages.parent_id 列未创建")
	}
	if n := countInt(t, db, "SELECT count(*) FROM pragma_table_info('chats') WHERE name = 'active_leaf_id'"); n != 1 {
		t.Fatal("chats.active_leaf_id 列未创建")
	}
	assertNullable(t, "messages[2].parent_id", readNullable(t, db, "SELECT parent_id FROM messages WHERE id = ?", 2), ptr(1))
	assertNullable(t, "chats[1].active_leaf_id", readNullable(t, db, "SELECT active_leaf_id FROM chats WHERE id = ?", 1), ptr(4))
	if n := countInt(t, db, "SELECT count(*) FROM metadata WHERE key = 'schema_version' AND value = '2'"); n != 1 {
		t.Fatal("metadata.schema_version 未提升到 2")
	}
	// chat FK 迁移同步完成：旧库的 messages 外键被移除（此后删除会话不再被阻止）
	if n := countInt(t, db, "SELECT count(*) FROM pragma_foreign_key_list('messages') WHERE \"table\" = 'chats'"); n != 0 {
		t.Fatal("messages 仍带 chats 外键，MigrateRemoveChatForeignKeys 未生效")
	}
	closeDB(t, db)

	// 模拟外部修改一条已回填记录，然后再次打开：metadata 表存在 → 迁移跳过、不修复
	raw := openRaw(t, dbFile)
	if err := raw.Exec("UPDATE messages SET parent_id = NULL WHERE id = 2").Error; err != nil {
		t.Fatalf("tamper: %v", err)
	}
	closeDB(t, raw)

	db2, err := storage.InitStorage(dataDir, "db.sqlite")
	if err != nil {
		t.Fatalf("second InitStorage: %v", err)
	}
	defer closeDB(t, db2)
	assertNullable(t, "messages[2].parent_id（二次打开后）", readNullable(t, db2, "SELECT parent_id FROM messages WHERE id = ?", 2), nil)
	if n := countInt(t, db2, "SELECT count(*) FROM metadata"); n != 2 {
		t.Fatalf("二次打开不应重写版本记录，metadata 应为 2 行，实际 %d", n)
	}
}

// TestInitStorageSkipsMigrationWhenMetadataViewConflicts 迁移失败（metadata 名字被
// 视图占用）时不阻塞数据库打开：ERROR 落日志、下次打开会重试；移除冲突后重试成功。
func TestInitStorageSkipsMigrationWhenMetadataViewConflicts(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".alkaid0")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbFile := filepath.Join(dataDir, "db.sqlite")

	legacy := openRaw(t, dbFile)
	migrate.ApplyLegacyFixtureForTest(t, legacy)
	if err := legacy.Exec("CREATE VIEW metadata AS SELECT 1 AS x").Error; err != nil {
		t.Fatalf("create view: %v", err)
	}
	closeDB(t, legacy)

	// 首次打开：迁移失败但数据库照常可用（回填未做、metadata 不存在）
	db, err := storage.InitStorage(dataDir, "db.sqlite")
	if err != nil {
		t.Fatalf("InitStorage should not fail on migration error: %v", err)
	}
	assertNullable(t, "messages[2].parent_id（迁移失败后）", readNullable(t, db, "SELECT parent_id FROM messages WHERE id = ?", 2), nil)
	if n := countInt(t, db, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'metadata'"); n != 0 {
		t.Fatal("迁移失败后不应存在 metadata 表")
	}
	closeDB(t, db)

	// 移除冲突视图后重新打开：迁移重试并成功
	raw := openRaw(t, dbFile)
	if err := raw.Exec("DROP VIEW metadata").Error; err != nil {
		t.Fatalf("drop view: %v", err)
	}
	closeDB(t, raw)

	db2, err := storage.InitStorage(dataDir, "db.sqlite")
	if err != nil {
		t.Fatalf("second InitStorage: %v", err)
	}
	defer closeDB(t, db2)
	assertNullable(t, "messages[2].parent_id（重试后）", readNullable(t, db2, "SELECT parent_id FROM messages WHERE id = ?", 2), ptr(1))
	if n := countInt(t, db2, "SELECT count(*) FROM metadata"); n != 2 {
		t.Fatalf("重试成功后 metadata 应有 2 行，实际 %d", n)
	}
}

func ptr(v uint64) *uint64 { return &v }
