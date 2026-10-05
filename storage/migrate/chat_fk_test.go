package migrate

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ==== 夹具与断言工具 ====

// seedLegacyWithTreeMigration 构造迁移前状态：v0.5.8 结构的旧库（messages 带
// fk_messages_chats 外键）已完成 MigrateToTree（metadata.schema_version = "1"），
// 此时唯一待处理的就是各子表残留的 chats 外键。
func seedLegacyWithTreeMigration(t *testing.T, dbPath string) {
	t.Helper()
	legacy := openRawDB(t, dbPath)
	applyLegacyFixture(t, legacy)
	closeRawDB(t, legacy)

	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)
	if err := MigrateToTree(db); err != nil {
		t.Fatalf("seed MigrateToTree: %v", err)
	}
	if ok, err := hasForeignKeyTo(db, "messages", "chats"); err != nil || !ok {
		t.Fatalf("夹具应带 fk_messages_chats（ok=%v err=%v）", ok, err)
	}
	if v := metadataValue(t, db, "schema_version"); v != "1" {
		t.Fatalf("夹具 schema_version = %q, want \"1\"", v)
	}
}

// assertChatFKPresence 断言 table 上指向 chats 的外键的存在性。
func assertChatFKPresence(t *testing.T, db *gorm.DB, table string, want bool) {
	t.Helper()
	has, err := hasForeignKeyTo(db, table, "chats")
	if err != nil {
		t.Fatalf("inspect foreign keys of %s: %v", table, err)
	}
	if has != want {
		t.Fatalf("%s 指向 chats 的外键存在 = %v, want %v", table, has, want)
	}
}

// openFKEnforcedDB 打开一个强制外键约束（foreign_keys=ON）的连接，
// 用于对照验证「删除会话行是否被外键阻止」。
func openFKEnforcedDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=foreign_keys(ON)"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open fk-enforced db %s: %v", path, err)
	}
	var fk int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&fk).Error; err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys 未启用（= %d）", fk)
	}
	return db
}

// ==== 用例 ====

// TestMigrateRemoveChatFKRemovesLegacyConstraint 核心用例：旧库的 messages 外键
// 被移除、数据（含孤儿行）完整保留、版本提升到 2。
//
// 同时做删除语义的对照实验（强制外键的连接）：迁移前删除被引用的会话行被外键
// 阻止；迁移后同样操作成功、消息数据按设计保留。
func TestMigrateRemoveChatFKRemovesLegacyConstraint(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	seedLegacyWithTreeMigration(t, dbPath)

	// 对照实验（迁移前）：chat 2 仍被消息引用，删除会被外键阻止。
	before := openFKEnforcedDB(t, dbPath)
	if err := before.Exec("DELETE FROM chats WHERE id = 2").Error; err == nil {
		t.Fatal("迁移前：删除被消息引用的会话行应被外键阻止")
	}
	closeRawDB(t, before)

	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)

	// 孤儿消息：会话已删、数据保留的场景（该连接未强制外键，可直接写入）
	if err := db.Exec("INSERT INTO messages (chat_id, delta) VALUES (999, 'orphan')").Error; err != nil {
		t.Fatalf("insert orphan message: %v", err)
	}
	rowsBefore := countRows(t, db, "SELECT count(*) FROM messages")

	if err := MigrateRemoveChatForeignKeys(db); err != nil {
		t.Fatalf("MigrateRemoveChatForeignKeys: %v", err)
	}

	// 1) 外键已移除
	assertChatFKPresence(t, db, "messages", false)
	// 2) 行数不变、数据保留（原 7 条 + 孤儿 1 条）
	if n := countRows(t, db, "SELECT count(*) FROM messages"); n != rowsBefore {
		t.Fatalf("迁移后行数 = %d, want %d（数据不得丢失）", n, rowsBefore)
	}
	var delta string
	if err := db.Raw("SELECT delta FROM messages WHERE id = 4").Row().Scan(&delta); err != nil {
		t.Fatalf("read delta: %v", err)
	}
	if delta != "m4" {
		t.Fatalf("messages[4].delta = %q, want \"m4\"", delta)
	}
	if n := countRows(t, db, "SELECT count(*) FROM messages WHERE chat_id = 999"); n != 1 {
		t.Fatalf("孤儿消息必须保留，实际 %d 行", n)
	}
	// 3) 版本提升到 2
	if v := metadataValue(t, db, "schema_version"); v != "2" {
		t.Fatalf("schema_version = %q, want \"2\"", v)
	}
	// 4) 索引按模型定义随重建恢复
	for _, idx := range []string{"idx_parent_id", "idx_chat_id"} {
		if !hasIndex(t, db, idx) {
			t.Fatalf("索引 %s 应按模型定义重建", idx)
		}
	}
	// 5) AUTOINCREMENT 序号不复用：下一条消息 id 必须大于历史最大值
	if err := db.Exec("INSERT INTO messages (chat_id, delta) VALUES (1, 'next')").Error; err != nil {
		t.Fatalf("insert next message: %v", err)
	}
	var maxID uint64
	if err := db.Raw("SELECT MAX(id) FROM messages").Scan(&maxID).Error; err != nil {
		t.Fatalf("read max id: %v", err)
	}
	if want := uint64(rowsBefore + 1); maxID != want {
		t.Fatalf("重建后 id 复用：MAX(id) = %d, want %d", maxID, want)
	}

	// 对照实验（迁移后）：同样强制外键的连接上，删除会话行成功、消息保留。
	after := openFKEnforcedDB(t, dbPath)
	if err := after.Exec("DELETE FROM chats WHERE id = 2").Error; err != nil {
		t.Fatalf("迁移后：删除会话行不应再被外键阻止: %v", err)
	}
	if n := countRows(t, after, "SELECT count(*) FROM messages WHERE chat_id = 2"); n != 2 {
		t.Fatalf("删除会话后消息应保留，实际 %d 行", n)
	}
	closeRawDB(t, after)

	// 6) 幂等：重跑直接跳过，数据不变
	if err := MigrateRemoveChatForeignKeys(db); err != nil {
		t.Fatalf("second call should be a no-op: %v", err)
	}
	if n := countRows(t, db, "SELECT count(*) FROM messages"); n != rowsBefore+1 {
		t.Fatalf("幂等重跑后行数 = %d, want %d", n, rowsBefore+1)
	}
}

// TestMigrateRemoveChatFKSkipsFreshDatabase 全新库：没有子表带外键，只写版本号。
func TestMigrateRemoveChatFKSkipsFreshDatabase(t *testing.T) {
	db := openRawDB(t, filepath.Join(t.TempDir(), "fresh.sqlite"))
	defer closeRawDB(t, db)

	if err := MigrateToTree(db); err != nil {
		t.Fatalf("MigrateToTree: %v", err)
	}
	if err := MigrateRemoveChatForeignKeys(db); err != nil {
		t.Fatalf("MigrateRemoveChatForeignKeys: %v", err)
	}
	if v := metadataValue(t, db, "schema_version"); v != "2" {
		t.Fatalf("schema_version = %q, want \"2\"", v)
	}
	for _, child := range chatChildTables {
		if !db.Migrator().HasTable(child.table) {
			continue
		}
		assertChatFKPresence(t, db, child.table, false)
	}
}

// TestMigrateRemoveChatFKSelfHealsVersionWithLeftoverFK 自愈：版本已是 2 但仍有
// 残留外键（外部改脏）时，迁移会再次重建而不是直接跳过。
func TestMigrateRemoveChatFKSelfHealsVersionWithLeftoverFK(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	seedLegacyWithTreeMigration(t, dbPath)

	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)

	if err := db.Exec("UPDATE metadata SET value = '2' WHERE key = 'schema_version'").Error; err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if err := MigrateRemoveChatForeignKeys(db); err != nil {
		t.Fatalf("MigrateRemoveChatForeignKeys: %v", err)
	}
	assertChatFKPresence(t, db, "messages", false)
}

// TestMigrateRemoveChatFKHandlesMultipleTablesWithoutMetadata 多张子表同时带外键
// 时逐张重建；metadata 表缺失（树迁移未完成）时核心工作仍会执行、版本留待补写。
func TestMigrateRemoveChatFKHandlesMultipleTablesWithoutMetadata(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy := openRawDB(t, dbPath)
	for _, stmt := range []string{
		"CREATE TABLE chats (id integer PRIMARY KEY AUTOINCREMENT, title text)",
		"CREATE TABLE scopes (`name` text, `enabled` numeric, `chat_id` integer, PRIMARY KEY (`chat_id`, `name`), CONSTRAINT `fk_scopes_chats` FOREIGN KEY (`chat_id`) REFERENCES `chats`(`id`))",
		"CREATE TABLE terminals (`id` integer PRIMARY KEY AUTOINCREMENT, `chat_id` integer, `history` blob, `title` text, CONSTRAINT `fk_terminals_chats` FOREIGN KEY (`chat_id`) REFERENCES `chats`(`id`))",
		"INSERT INTO chats (id, title) VALUES (1, 'legacy')",
		"INSERT INTO scopes (`name`, `enabled`, `chat_id`) VALUES ('default', 1, 1)",
		"INSERT INTO terminals (`chat_id`, `title`) VALUES (1, 'shell')",
	} {
		if err := legacy.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	closeRawDB(t, legacy)

	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)

	if err := MigrateRemoveChatForeignKeys(db); err != nil {
		t.Fatalf("MigrateRemoveChatForeignKeys: %v", err)
	}

	for _, tc := range []struct{ table, countQuery string }{
		{"scopes", "SELECT count(*) FROM scopes"},
		{"terminals", "SELECT count(*) FROM terminals"},
	} {
		assertChatFKPresence(t, db, tc.table, false)
		if n := countRows(t, db, tc.countQuery); n != 1 {
			t.Fatalf("%s 数据应保留，实际 %d 行", tc.table, n)
		}
	}
	if db.Migrator().HasTable("metadata") {
		t.Fatal("metadata 缺失时不应被创建（版本记录留待下次启动补写）")
	}
}

// TestMigrateRemoveChatFKRollsBackOnFailure 事务性：重建中途失败即整体回滚
// （外键、数据、版本号保持原样），移除故障后重试成功。
//
// 故障注入：先建一个与重建临时表同名的 VIEW。DROP TABLE IF EXISTS 不会删除视图，
// 随后的 CREATE TABLE <tmp> 会撞名失败（若 DROP 本身报错，同样在事务内失败）。
func TestMigrateRemoveChatFKRollsBackOnFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	seedLegacyWithTreeMigration(t, dbPath)

	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)

	tmpView := "messages" + chatFKTmpSuffix
	if err := db.Exec("CREATE VIEW " + tmpView + " AS SELECT 1 AS x").Error; err != nil {
		t.Fatalf("create view: %v", err)
	}

	if err := MigrateRemoveChatForeignKeys(db); err == nil {
		t.Fatal("临时表名被占用时应返回错误")
	}

	// 回滚断言：外键仍在、行数不变、版本仍为 1
	assertChatFKPresence(t, db, "messages", true)
	if n := countRows(t, db, "SELECT count(*) FROM messages"); n != 7 {
		t.Fatalf("回滚后行数 = %d, want 7", n)
	}
	if v := metadataValue(t, db, "schema_version"); v != "1" {
		t.Fatalf("回滚后 schema_version = %q, want \"1\"", v)
	}

	// 移除故障后重试应成功
	if err := db.Exec("DROP VIEW " + tmpView).Error; err != nil {
		t.Fatalf("drop view: %v", err)
	}
	if err := MigrateRemoveChatForeignKeys(db); err != nil {
		t.Fatalf("retry after fix: %v", err)
	}
	assertChatFKPresence(t, db, "messages", false)
	if v := metadataValue(t, db, "schema_version"); v != "2" {
		t.Fatalf("重试成功后 schema_version = %q, want \"2\"", v)
	}
}

// TestMigrateRemoveChatFKNilDB nil 句柄直接报错，避免启动路径静默跳过。
func TestMigrateRemoveChatFKNilDB(t *testing.T) {
	if err := MigrateRemoveChatForeignKeys(nil); err == nil {
		t.Fatal("nil db 应返回错误")
	}
}
