package migrate

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// ==== 测试夹具：迁移前（v0.5.8）的真实库结构 ====

// legacyChatsDDL / legacyMessagesDDL 旧版运行库的实际建表语句（取自 sqlite_master
// dump）：没有 parent_id / active_leaf_id 列，也没有 idx_chat_id / idx_parent_id 索引，
// messages 已带 fk_messages_chats 外键。
const legacyChatsDDL = "CREATE TABLE `chats` (`id` integer PRIMARY KEY AUTOINCREMENT,`last_model_id` integer,`now_agent` text,`root` text,`trace_id` integer,`state` integer,`title` text,`ai_title` text,`reasoning_effort` text,`task` text,`hidden` numeric NOT NULL DEFAULT false,`updated_at` datetime)"

const legacyMessagesDDL = "CREATE TABLE `messages` (`id` integer PRIMARY KEY AUTOINCREMENT,`chat_id` integer,`agent_id` text,`delta` text,`summary` text,`thinking_delta` text,`refers` blob,`tool_calling_json_string` text,`tool_calling_content` text,`time` integer,`model_name` text,`model_id` integer,`type` integer,`prompt_tokens` integer,`completion_tokens` integer,`total_tokens` integer,`cached_tokens` integer,CONSTRAINT `fk_messages_chats` FOREIGN KEY (`chat_id`) REFERENCES `chats`(`id`))"

// applyLegacyFixture 构造迁移前的库并写入夹具数据。
//
// 数据布局（消息 id 按插入顺序自增）：
//
//	chat 1: m1 m2 m3 m4（线性链 1→2→3→4）
//	chat 2: m5 m6（线性链 5→6；两条消息 time 相同，强调排序只依赖 id）
//	chat 3: 空会话
//	chat 4: m7（单条消息 = 根节点）
func applyLegacyFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, ddl := range []string{legacyChatsDDL, legacyMessagesDDL} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	for _, c := range []struct {
		id    uint32
		title string
	}{{1, "chat-a"}, {2, "chat-b"}, {3, "chat-empty"}, {4, "chat-single"}} {
		if err := db.Exec("INSERT INTO chats (id, title) VALUES (?, ?)", c.id, c.title).Error; err != nil {
			t.Fatalf("insert legacy chat: %v", err)
		}
	}
	for _, m := range []struct {
		chatID uint32
		delta  string
	}{
		{1, "m1"}, {1, "m2"}, {1, "m3"}, {1, "m4"},
		{2, "m5"}, {2, "m6"},
		{4, "m7"},
	} {
		if err := db.Exec("INSERT INTO messages (chat_id, delta, time, type, model_name) VALUES (?, ?, 1000, 0, 'test-model')",
			m.chatID, m.delta).Error; err != nil {
			t.Fatalf("insert legacy message: %v", err)
		}
	}
}

// ==== 通用工具 ====

func openRawDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db %s: %v", path, err)
	}
	return db
}

func closeRawDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}
}

// readNullableU64 读取一个可空整型列，NULL 返回 nil。
func readNullableU64(t *testing.T, db *gorm.DB, query string, args ...any) *uint64 {
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

func ptrU64(v uint64) *uint64 { return &v }

func formatNullable(v *uint64) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%d", *v)
}

// assertNullableU64 断言实际值等于期望值（nil 表示 NULL）。
func assertNullableU64(t *testing.T, what string, got, want *uint64) {
	t.Helper()
	equal := (got == nil && want == nil) || (got != nil && want != nil && *got == *want)
	if !equal {
		t.Fatalf("%s = %s, want %s", what, formatNullable(got), formatNullable(want))
	}
}

func countRows(t *testing.T, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(query, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func metadataValue(t *testing.T, db *gorm.DB, key string) string {
	t.Helper()
	var v string
	row := db.Raw("SELECT value FROM metadata WHERE key = ?", key).Row()
	if row == nil {
		t.Fatalf("metadata[%s] row handle missing", key)
	}
	if err := row.Scan(&v); err != nil {
		t.Fatalf("read metadata[%s]: %v", key, err)
	}
	return v
}

func hasIndex(t *testing.T, db *gorm.DB, name string) bool {
	t.Helper()
	return countRows(t, db, "SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name) > 0
}

// ==== 用例 ====

// TestMigrateToTreeBackfillsLinearHistory 核心用例：把 v0.5.8 的线性历史迁移为树。
func TestMigrateToTreeBackfillsLinearHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy := openRawDB(t, dbPath)
	applyLegacyFixture(t, legacy)
	closeRawDB(t, legacy)

	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)

	if err := MigrateToTree(db); err != nil {
		t.Fatalf("MigrateToTree: %v", err)
	}

	// 1) 新列与索引
	if !db.Migrator().HasColumn(&structs.Messages{}, "parent_id") {
		t.Fatal("messages.parent_id 列未创建")
	}
	if !db.Migrator().HasColumn(&structs.Chats{}, "active_leaf_id") {
		t.Fatal("chats.active_leaf_id 列未创建")
	}
	for _, idx := range []string{"idx_parent_id", "idx_chat_id"} {
		if !hasIndex(t, db, idx) {
			t.Fatalf("索引 %s 未创建", idx)
		}
	}

	// 2) 旧数据保留（列不重命名、不删除）
	if n := countRows(t, db, "SELECT count(*) FROM messages"); n != 7 {
		t.Fatalf("迁移后消息行数 = %d, want 7（旧数据不得丢失）", n)
	}
	var delta string
	if err := db.Raw("SELECT delta FROM messages WHERE id = 4").Row().Scan(&delta); err != nil {
		t.Fatalf("read delta: %v", err)
	}
	if delta != "m4" {
		t.Fatalf("messages[4].delta = %q, want \"m4\"", delta)
	}

	// 3) parent_id 回填：父 = 同会话内上一条消息；每个会话最小 id 保持 NULL 根节点
	wantParents := map[uint64]*uint64{
		1: nil, 2: ptrU64(1), 3: ptrU64(2), 4: ptrU64(3),
		5: nil, 6: ptrU64(5),
		7: nil,
	}
	for id, want := range wantParents {
		got := readNullableU64(t, db, "SELECT parent_id FROM messages WHERE id = ?", id)
		assertNullableU64(t, fmt.Sprintf("messages[%d].parent_id", id), got, want)
	}

	// 4) active_leaf_id 回填：各会话最新消息；空会话保持 NULL
	wantLeaves := map[uint32]*uint64{
		1: ptrU64(4), 2: ptrU64(6), 3: nil, 4: ptrU64(7),
	}
	for id, want := range wantLeaves {
		got := readNullableU64(t, db, "SELECT active_leaf_id FROM chats WHERE id = ?", id)
		assertNullableU64(t, fmt.Sprintf("chats[%d].active_leaf_id", id), got, want)
	}

	// 5) metadata 版本记录
	if v := metadataValue(t, db, "schema_version"); v != "1" {
		t.Fatalf("metadata.schema_version = %q, want \"1\"", v)
	}
	parsed, err := time.Parse(time.RFC3339, metadataValue(t, db, "migrated_at"))
	if err != nil {
		t.Fatalf("metadata.migrated_at 不是 RFC3339 时间戳: %v", err)
	}
	if diff := time.Since(parsed); diff > time.Hour || diff < -time.Hour {
		t.Fatalf("metadata.migrated_at 与当前时间偏差过大: %v", diff)
	}
	if n := countRows(t, db, "SELECT count(*) FROM metadata"); n != 2 {
		t.Fatalf("metadata 表应有 2 条版本记录，实际 %d", n)
	}

	// 6) 完整性校验全部通过
	if issues := verifyTree(db); len(issues) != 0 {
		t.Fatalf("迁移后完整性校验未通过: %v", issues)
	}
}

// TestMigrateToTreeSkipsWhenMetadataExists 「metadata 表存在 = 已迁移」：
// 即使表中没有任何版本记录也不再执行（不补列、不回填、不写版本）。
func TestMigrateToTreeSkipsWhenMetadataExists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy := openRawDB(t, dbPath)
	applyLegacyFixture(t, legacy)
	if err := legacy.Exec("CREATE TABLE metadata (`key` text PRIMARY KEY, `value` text, `updated_at` datetime)").Error; err != nil {
		t.Fatalf("create metadata: %v", err)
	}
	closeRawDB(t, legacy)

	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)

	if err := MigrateToTree(db); err != nil {
		t.Fatalf("MigrateToTree: %v", err)
	}

	if db.Migrator().HasColumn(&structs.Messages{}, "parent_id") {
		t.Fatal("metadata 表存在时不应执行迁移：parent_id 列不该被创建")
	}
	if db.Migrator().HasColumn(&structs.Chats{}, "active_leaf_id") {
		t.Fatal("metadata 表存在时不应执行迁移：active_leaf_id 列不该被创建")
	}
	if n := countRows(t, db, "SELECT count(*) FROM metadata"); n != 0 {
		t.Fatalf("metadata 表应保持空表，实际 %d 行", n)
	}
}

// TestMigrateToTreeIdempotent 迁移只执行一次：metadata 建好后重跑不覆盖任何已回填的值。
func TestMigrateToTreeIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy := openRawDB(t, dbPath)
	applyLegacyFixture(t, legacy)
	closeRawDB(t, legacy)

	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)

	if err := MigrateToTree(db); err != nil {
		t.Fatalf("first MigrateToTree: %v", err)
	}
	// 篡改一条已回填的记录（模拟外部修改），再跑一遍迁移
	if err := db.Exec("UPDATE messages SET parent_id = NULL WHERE id = 4").Error; err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if err := MigrateToTree(db); err != nil {
		t.Fatalf("second MigrateToTree: %v", err)
	}

	got := readNullableU64(t, db, "SELECT parent_id FROM messages WHERE id = ?", 4)
	assertNullableU64(t, "messages[4].parent_id（重跑后）", got, nil)
	if n := countRows(t, db, "SELECT count(*) FROM metadata"); n != 2 {
		t.Fatalf("重跑不应再写版本记录，metadata 应为 2 行，实际 %d", n)
	}
	// 其余回填不受影响
	leaf := readNullableU64(t, db, "SELECT active_leaf_id FROM chats WHERE id = ?", 1)
	assertNullableU64(t, "chats[1].active_leaf_id（重跑后）", leaf, ptrU64(4))
}

// TestMigrateToTreeFreshDatabase 全新数据库：一切从零建起，metadata 版本记录齐备。
func TestMigrateToTreeFreshDatabase(t *testing.T) {
	db := openRawDB(t, filepath.Join(t.TempDir(), "fresh.sqlite"))
	defer closeRawDB(t, db)

	if err := MigrateToTree(db); err != nil {
		t.Fatalf("MigrateToTree: %v", err)
	}
	for _, tbl := range []string{"chats", "messages", "metadata"} {
		if !db.Migrator().HasTable(tbl) {
			t.Fatalf("表 %s 未创建", tbl)
		}
	}
	if v := metadataValue(t, db, "schema_version"); v != "1" {
		t.Fatalf("metadata.schema_version = %q, want \"1\"", v)
	}
	if n := countRows(t, db, "SELECT count(*) FROM metadata"); n != 2 {
		t.Fatalf("metadata 表应有 2 条版本记录，实际 %d", n)
	}
	if issues := verifyTree(db); len(issues) != 0 {
		t.Fatalf("空库迁移后完整性校验不应报错: %v", issues)
	}
}

// TestMigrateToTreeRollsBackOnFailure 事务性：任一步失败即整体回滚（建列、索引、
// 建表全部撤销），metadata 表随之消失，移除故障后可重试成功。
//
// 故障注入：先建一个同名 VIEW。HasTable 只认 table 类型对象，视图不会被当作
// 「已迁移」标记，而 CREATE TABLE metadata 会失败。
func TestMigrateToTreeRollsBackOnFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
	db := openRawDB(t, dbPath)
	defer closeRawDB(t, db)
	applyLegacyFixture(t, db)

	if err := db.Exec("CREATE VIEW metadata AS SELECT 1 AS x").Error; err != nil {
		t.Fatalf("create view: %v", err)
	}
	if db.Migrator().HasTable("metadata") {
		t.Fatal("视图不应被 HasTable 当作表")
	}

	if err := MigrateToTree(db); err == nil {
		t.Fatal("metadata 建表冲突时应返回错误")
	}

	// 回滚断言：DDL 全部撤销，只剩夹具的旧结构
	if db.Migrator().HasColumn(&structs.Messages{}, "parent_id") {
		t.Fatal("迁移失败后 parent_id 列应随事务回滚")
	}
	if db.Migrator().HasColumn(&structs.Chats{}, "active_leaf_id") {
		t.Fatal("迁移失败后 active_leaf_id 列应随事务回滚")
	}
	if hasIndex(t, db, "idx_parent_id") || hasIndex(t, db, "idx_chat_id") {
		t.Fatal("迁移失败后索引应随事务回滚")
	}
	if n := countRows(t, db, "SELECT count(*) FROM messages"); n != 7 {
		t.Fatalf("回滚后旧数据必须原样保留，实际 %d 行", n)
	}

	// 移除故障后重试应成功（对应「下次启动会重新尝试迁移」）
	if err := db.Exec("DROP VIEW metadata").Error; err != nil {
		t.Fatalf("drop view: %v", err)
	}
	if err := MigrateToTree(db); err != nil {
		t.Fatalf("retry after fix: %v", err)
	}
	if !db.Migrator().HasColumn(&structs.Messages{}, "parent_id") {
		t.Fatal("重试迁移后 parent_id 列应创建")
	}
	got := readNullableU64(t, db, "SELECT parent_id FROM messages WHERE id = ?", 2)
	assertNullableU64(t, "messages[2].parent_id（重试后）", got, ptrU64(1))
}

// TestMigrateToTreeNilDB nil 句柄直接报错，避免启动路径静默跳过。
func TestMigrateToTreeNilDB(t *testing.T) {
	if err := MigrateToTree(nil); err == nil {
		t.Fatal("nil db 应返回错误")
	}
}

// TestVerifyTreeDetectsViolations 四组校验查询必须能抓到对应的不变量破坏。
func TestVerifyTreeDetectsViolations(t *testing.T) {
	cases := []struct {
		name    string
		mutate  string
		wantTag string
	}{
		{"多根节点", "UPDATE messages SET parent_id = NULL WHERE id = 3", "[root-count]"},
		{"跨会话父引用", "UPDATE messages SET parent_id = 1 WHERE id = 5", "[cross-chat-parent]"},
		{"父 ID 不小于子 ID", "UPDATE messages SET parent_id = 6 WHERE id = 5", "[parent-order]"},
		{"活跃叶子指向不存在的消息", "UPDATE chats SET active_leaf_id = 999 WHERE id = 1", "[active-leaf]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")
			seed := openRawDB(t, dbPath)
			applyLegacyFixture(t, seed)
			closeRawDB(t, seed)

			db := openRawDB(t, dbPath)
			defer closeRawDB(t, db)
			if err := MigrateToTree(db); err != nil {
				t.Fatalf("seed migrate: %v", err)
			}
			if issues := verifyTree(db); len(issues) != 0 {
				t.Fatalf("初始数据不应有问题: %v", issues)
			}

			if err := db.Exec(tc.mutate).Error; err != nil {
				t.Fatalf("mutate: %v", err)
			}
			issues := verifyTree(db)
			if len(issues) == 0 {
				t.Fatalf("破坏「%s」后校验应报错", tc.name)
			}
			if joined := strings.Join(issues, "\n"); !strings.Contains(joined, tc.wantTag) {
				t.Fatalf("校验结果未包含 %s: %v", tc.wantTag, issues)
			}
		})
	}
}

// TestMessagesTreeFieldsRoundTrip 新字段经 GORM 正常读写：根节点存 NULL、分支节点存父 ID。
func TestMessagesTreeFieldsRoundTrip(t *testing.T) {
	db := openRawDB(t, filepath.Join(t.TempDir(), "fresh.sqlite"))
	defer closeRawDB(t, db)
	if err := MigrateToTree(db); err != nil {
		t.Fatalf("MigrateToTree: %v", err)
	}

	chat := structs.Chats{Title: "roundtrip"}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	root := structs.Messages{ChatID: chat.ID, Delta: "root", Type: structs.MessagesRoleUser}
	if err := db.Create(&root).Error; err != nil {
		t.Fatalf("create root message: %v", err)
	}
	child := structs.Messages{ChatID: chat.ID, ParentID: &root.ID, Delta: "child", Type: structs.MessagesRoleAgent}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create child message: %v", err)
	}
	if err := db.Model(&structs.Chats{}).Where("id = ?", chat.ID).Update("active_leaf_id", child.ID).Error; err != nil {
		t.Fatalf("update active_leaf_id: %v", err)
	}

	var gotRoot structs.Messages
	if err := db.First(&gotRoot, root.ID).Error; err != nil {
		t.Fatalf("read root: %v", err)
	}
	if gotRoot.ParentID != nil {
		t.Fatalf("根节点 parent_id 应为 NULL，实际 %d", *gotRoot.ParentID)
	}
	var gotChild structs.Messages
	if err := db.First(&gotChild, child.ID).Error; err != nil {
		t.Fatalf("read child: %v", err)
	}
	if gotChild.ParentID == nil || *gotChild.ParentID != root.ID {
		t.Fatalf("子节点 parent_id 应为 %d，实际 %v", root.ID, gotChild.ParentID)
	}
	var gotChat structs.Chats
	if err := db.First(&gotChat, chat.ID).Error; err != nil {
		t.Fatalf("read chat: %v", err)
	}
	if gotChat.ActiveLeafID == nil || *gotChat.ActiveLeafID != child.ID {
		t.Fatalf("active_leaf_id 应为 %d，实际 %v", child.ID, gotChat.ActiveLeafID)
	}
}

// TestMetadataTableName Metadata 表名必须是小写单数 metadata，不走 GORM 复数化。
func TestMetadataTableName(t *testing.T) {
	if got := (structs.Metadata{}).TableName(); got != "metadata" {
		t.Fatalf("Metadata 表名 = %q, want \"metadata\"", got)
	}
}
