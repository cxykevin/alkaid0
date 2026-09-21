package storage

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// closeGormDB 关闭 gorm 底层的 sql.DB，避免临时文件在 Windows 上被占用。
func closeGormDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}
}

// countByChat 统计某会话在指定表中的行数。
func countByChat(t *testing.T, db *gorm.DB, model any, chatID uint32) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Where("chat_id = ?", chatID).Count(&n).Error; err != nil {
		t.Fatalf("count %T: %v", model, err)
	}
	return n
}

// TestInitStorageMemoryPathOpensInMemoryDatabase 回归测试：以 :memory: 结尾的路径
// 必须打开真正的内存数据库。
//
// 修复前 InitStorage(dir, ":memory:") 拼出的是 "<dir>/:memory:"：代码只把它当内存
// 模式处理（建目录），却把这个路径原样交给 SQLite，而 SQLite 只把恰好等于 ":memory:"
// 的名字当内存库，于是目录里真实落盘了 :memory: / :memory:-wal / :memory:-shm 三个文件，
// 所谓"内存库"其实是磁盘文件。
func TestInitStorageMemoryPathOpensInMemoryDatabase(t *testing.T) {
	// CI 会设置 ALKAID0_TEST_LESS_MEMORY_MODE=true，该开关会把任何内存库请求
	// 主动降级为临时文件库（见 InitDB）。本用例验证的是"未开启该降级开关时
	// :memory: 必须真的是内存库"，因此显式清空该变量。
	t.Setenv("ALKAID0_TEST_LESS_MEMORY_MODE", "")

	dir := t.TempDir()
	db, err := InitStorage(dir, ":memory:")
	if err != nil {
		t.Fatalf("InitStorage: %v", err)
	}
	defer closeGormDB(t, db)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("内存数据库不应在磁盘留下文件，实际: %v", names)
	}

	var dbs []struct {
		Name string `gorm:"column:name"`
		File string `gorm:"column:file"`
	}
	if err := db.Raw("PRAGMA database_list").Scan(&dbs).Error; err != nil {
		t.Fatalf("PRAGMA database_list: %v", err)
	}
	for _, d := range dbs {
		if d.Name == "main" && d.File != "" {
			t.Fatalf("main 数据库应为内存库（file 列为空），实际 file=%q", d.File)
		}
	}
}

// TestInitDBSetsPragmasOnEveryNewConnection 回归测试：per-connection PRAGMA 必须在
// 连接池回收后新建的连接上依然生效。
//
// 修复前这些 PRAGMA 是 InitStorage 里的一次性 db.Exec，只对当时那条连接有效；
// MaxIdleConns=0 强制回收连接后，foreign_keys 会退回默认 0、cache_size 退回 -2000。
func TestInitDBSetsPragmasOnEveryNewConnection(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer closeGormDB(t, db)

	// 强制连接池回收：MaxIdleConns=0 后这条空闲连接不会被保留，
	// 下一条语句必然跑在新建的连接上
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxIdleConns(0)
	var one int
	if err := db.Raw("SELECT 1").Scan(&one).Error; err != nil {
		t.Fatalf("warm up: %v", err)
	}

	var fk int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&fk).Error; err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("连接回收后新建的连接必须仍有 foreign_keys=ON，实际 %d", fk)
	}
	var cacheSize int
	if err := db.Raw("PRAGMA cache_size").Scan(&cacheSize).Error; err != nil {
		t.Fatalf("PRAGMA cache_size: %v", err)
	}
	if cacheSize != -512 {
		t.Fatalf("连接回收后 cache_size 应为 -512，实际 %d", cacheSize)
	}

	// 行为验证：外键约束在新连接上确实生效（仅 PRAGMA 读数为 1 不足以证明）
	chat := structs.Chats{}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Exec("INSERT INTO scopes (chat_id, name, enabled) VALUES (?, ?, ?)", chat.ID+1000, "orphan", true).Error; err == nil {
		t.Fatal("外键约束未生效：允许插入引用不存在会话的 scopes 行")
	}
}

// TestInitStorageRunsMaintenanceOncePerDatabaseFile 回归测试：VACUUM/ANALYZE 不得
// 每次打开数据库都执行。
//
// 这两个语句都会扫描/重写整个数据库文件，而 InitStorage 会被 server/actions 的
// loadDB 反复调用（每次打开会话数据库、每条后台 workflow 事件）。这里用 ANALYZE
// 的可观测副作用（sqlite_stat1 统计行）判断它是否被执行。
func TestInitStorageRunsMaintenanceOncePerDatabaseFile(t *testing.T) {
	dir := t.TempDir()

	// 第一次打开：会执行一次 VACUUM/ANALYZE
	db1, err := InitStorage(dir, "db.sqlite")
	if err != nil {
		t.Fatalf("first InitStorage: %v", err)
	}
	// 造一张带索引且有数据的表：ANALYZE 会为它的索引写入 sqlite_stat1 统计行
	for _, stmt := range []string{
		"CREATE TABLE IF NOT EXISTS bloat (b BLOB)",
		"CREATE INDEX IF NOT EXISTS idx_bloat_b ON bloat(b)",
		"INSERT INTO bloat (b) VALUES (zeroblob(100000)), (zeroblob(100000)), (zeroblob(100000))",
	} {
		if err := db1.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	closeGormDB(t, db1)

	// 第二次打开同一个文件：不得再执行 ANALYZE
	db2, err := InitStorage(dir, "db.sqlite")
	if err != nil {
		t.Fatalf("second InitStorage: %v", err)
	}
	defer closeGormDB(t, db2)

	var analyzed int64
	if err := db2.Raw("SELECT count(*) FROM sqlite_stat1 WHERE tbl = 'bloat'").Scan(&analyzed).Error; err != nil {
		t.Fatalf("read sqlite_stat1: %v", err)
	}
	if analyzed != 0 {
		t.Fatalf("第二次打开数据库不应再次执行 ANALYZE，但 sqlite_stat1 中已有 bloat 的 %d 行统计", analyzed)
	}

	// 对照组：强制再维护一次确实会写入统计行，证明上面的断言能区分"跑过 / 没跑"
	runMaintenanceOnce(db2, filepath.Join(dir, "forced-control.sqlite"))
	if err := db2.Raw("SELECT count(*) FROM sqlite_stat1 WHERE tbl = 'bloat'").Scan(&analyzed).Error; err != nil {
		t.Fatalf("read sqlite_stat1 after forced maintenance: %v", err)
	}
	if analyzed == 0 {
		t.Fatal("对照组失败：ANALYZE 未写入 bloat 的统计行，用例无法区分维护是否执行")
	}
}

// TestSaveGlobalConfigsRollsBackOnCreateFailure 回归测试：Configs 的"清空 + 重插"
// 必须在同一个事务里。
//
// 修复前 DELETE 与 CREATE 是两条独立语句：Create 失败时 DELETE 已经提交，配置表变空，
// 下次启动读回零值，用户的全部设置静默丢失。
// 配置表没有唯一约束，这里用 GORM 回调注入一次 Create 失败来复现"中途失败"。
func TestSaveGlobalConfigsRollsBackOnCreateFailure(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer closeGormDB(t, db)

	oldConfig := GlobalConfig
	defer func() { GlobalConfig = oldConfig }()

	GlobalConfig = structs.Configs{LastChatID: 42}
	if err := SaveGlobalConfigs(db); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	var failNext atomic.Bool
	db.Callback().Create().Before("gorm:create").Register("storage_test:fail_configs_create", func(tx *gorm.DB) {
		if failNext.Load() && tx.Statement.Schema != nil && tx.Statement.Schema.Table == "configs" {
			tx.AddError(errors.New("injected configs create failure"))
		}
	})

	GlobalConfig = structs.Configs{LastChatID: 99}
	failNext.Store(true)
	saveErr := SaveGlobalConfigs(db)
	failNext.Store(false)
	if saveErr == nil {
		t.Fatal("注入的 Create 失败应被返回")
	}

	GlobalConfig = structs.Configs{}
	if err := ReadGlobalConfigs(db); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if GlobalConfig.LastChatID != 42 {
		t.Fatalf("失败的保存必须整体回滚，读回应是旧配置 42，实际 %d", GlobalConfig.LastChatID)
	}
	var rows int64
	if err := db.Model(&structs.Configs{}).Count(&rows).Error; err != nil {
		t.Fatalf("count configs: %v", err)
	}
	if rows != 1 {
		t.Fatalf("配置表应保留原来的 1 行，实际 %d 行", rows)
	}
}

// legacyScopesDDL 修复前 scopes 表的实际结构：name 单列主键，chat_id 只是普通列。
const legacyScopesDDL = "CREATE TABLE scopes (name text PRIMARY KEY, enabled numeric, chat_id integer)"

// TestInitDBMigratesLegacyScopesPrimaryKey 回归测试：旧库的 scopes 表必须升级为
// (chat_id, name) 复合主键。
//
// GORM 的 AutoMigrate 永远不会修改已有表的主键（gorm/migrator.MigrateColumn 里所有
// 主键差异都被跳过），因此旧库升级后 scopes.name 仍是单列唯一键：第二个会话保存
// 同名 scope 会直接拿到 "UNIQUE constraint failed: scopes.name"。
func TestInitDBMigratesLegacyScopesPrimaryKey(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.sqlite")

	legacy, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	for _, stmt := range []string{
		"CREATE TABLE chats (id integer PRIMARY KEY AUTOINCREMENT, title text)",
		legacyScopesDDL,
		"INSERT INTO chats (id, title) VALUES (1, 'legacy-a'), (2, 'legacy-b')",
		"INSERT INTO scopes (name, enabled, chat_id) VALUES ('default', 1, 1)",
	} {
		if err := legacy.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	closeGormDB(t, legacy)

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("upgrade legacy db: %v", err)
	}
	defer closeGormDB(t, db)

	// 1) 主键必须是 (chat_id, name)
	var cols []struct {
		Name string `gorm:"column:name"`
		PK   int    `gorm:"column:pk"`
	}
	if err := db.Raw("PRAGMA table_info(scopes)").Scan(&cols).Error; err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	pk := map[string]int{}
	for _, c := range cols {
		pk[c.Name] = c.PK
	}
	if pk["chat_id"] == 0 || pk["name"] == 0 {
		t.Fatalf("scopes 主键应为 (chat_id, name)，实际 pk 标记 chat_id=%d name=%d", pk["chat_id"], pk["name"])
	}

	// 2) 旧数据必须保留
	var enabled bool
	if err := db.Raw("SELECT enabled FROM scopes WHERE chat_id = 1 AND name = 'default'").Scan(&enabled).Error; err != nil {
		t.Fatalf("read legacy scope: %v", err)
	}
	if !enabled {
		t.Fatal("迁移后 chat 1 的 default scope 应为 enabled=true")
	}

	// 3) 第二个会话可以保存同名 scope（修复前 UNIQUE constraint failed: scopes.name）
	if err := db.Exec("INSERT INTO scopes (name, enabled, chat_id) VALUES ('default', 0, 2)").Error; err != nil {
		t.Fatalf("第二个会话保存同名 scope 失败: %v", err)
	}
	var count int64
	if err := db.Raw("SELECT count(*) FROM scopes WHERE name = 'default'").Scan(&count).Error; err != nil {
		t.Fatalf("count scopes: %v", err)
	}
	if count != 2 {
		t.Fatalf("两个会话应各有一行 default scope，实际 %d 行", count)
	}

	// 4) 重建后的表仍须保留 chat_id 外键
	if err := db.Exec("INSERT INTO scopes (name, enabled, chat_id) VALUES ('orphan', 1, 99999)").Error; err == nil {
		t.Fatal("重建 scopes 表后外键约束丢失：允许插入引用不存在会话的行")
	}
}

// TestChatDeleteCascadesClassifySegmentsAndWorkflows 回归测试：删除会话必须级联清掉
// classify_segments / workflows / workflow_events。
//
// 这三张表此前既没有外键、也没有任何删除路径：会话删除后行永久残留
// （classify_segments.text 里是整段用户输入的副本，无界增长）。
func TestChatDeleteCascadesClassifySegmentsAndWorkflows(t *testing.T) {
	db, err := InitDB(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer closeGormDB(t, db)

	chat := structs.Chats{}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&structs.ClassifySegment{ChatID: chat.ID, MessageID: 1, Label: "prompt", Text: "hello"}).Error; err != nil {
		t.Fatalf("create classify segment: %v", err)
	}
	if err := db.Create(&structs.Workflows{WorkflowID: "wf-1", ChatID: chat.ID, RunID: "run-1", Status: "running"}).Error; err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if err := db.Create(&structs.WorkflowEvents{WorkflowID: "wf-1", ChatID: chat.ID, Sequence: 1, Type: "node"}).Error; err != nil {
		t.Fatalf("create workflow event: %v", err)
	}

	if err := db.Delete(&structs.Chats{}, chat.ID).Error; err != nil {
		t.Fatalf("delete chat: %v", err)
	}

	if n := countByChat(t, db, &structs.ClassifySegment{}, chat.ID); n != 0 {
		t.Fatalf("删除会话后 classify_segments 应被级联清理，实际残留 %d 行", n)
	}
	if n := countByChat(t, db, &structs.Workflows{}, chat.ID); n != 0 {
		t.Fatalf("删除会话后 workflows 应被级联清理，实际残留 %d 行", n)
	}
	if n := countByChat(t, db, &structs.WorkflowEvents{}, chat.ID); n != 0 {
		t.Fatalf("删除会话后 workflow_events 应被级联清理，实际残留 %d 行", n)
	}
}

// TestInitDBAddsCascadeForeignKeysToLegacyTables 回归测试：历史数据库（三张子表没有
// 外键）升级后必须补上级联外键。
//
// 用 DisableForeignKeyConstraintWhenMigrating 建出与修复前完全一致的"无外键" schema，
// 再走 InitDB 的正常升级路径（AutoMigrate 为已存在的表补建约束时会重建表）。
func TestInitDBAddsCascadeForeignKeysToLegacyTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.sqlite")

	legacy, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if err := legacy.AutoMigrate(structs.Tables...); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	var cascadeTables int64
	if err := legacy.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND sql LIKE '%ON DELETE CASCADE%'").Scan(&cascadeTables).Error; err != nil {
		t.Fatalf("inspect legacy schema: %v", err)
	}
	if cascadeTables != 0 {
		t.Fatalf("旧 schema 构造失败：不应存在级联外键，实际 %d 张表", cascadeTables)
	}
	closeGormDB(t, legacy)

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("upgrade legacy db: %v", err)
	}
	defer closeGormDB(t, db)

	if err := db.Raw("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND sql LIKE '%ON DELETE CASCADE%'").Scan(&cascadeTables).Error; err != nil {
		t.Fatalf("inspect upgraded schema: %v", err)
	}
	if cascadeTables < 3 {
		t.Fatalf("升级后应至少有 3 张表带 ON DELETE CASCADE，实际 %d 张", cascadeTables)
	}

	chat := structs.Chats{}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	if err := db.Create(&structs.ClassifySegment{ChatID: chat.ID, MessageID: 1, Label: "log", Text: "boom"}).Error; err != nil {
		t.Fatalf("create classify segment: %v", err)
	}
	if err := db.Create(&structs.Workflows{WorkflowID: "wf-2", ChatID: chat.ID, RunID: "run-2", Status: "running"}).Error; err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if err := db.Create(&structs.WorkflowEvents{WorkflowID: "wf-2", ChatID: chat.ID, Sequence: 1, Type: "node"}).Error; err != nil {
		t.Fatalf("create workflow event: %v", err)
	}
	if err := db.Delete(&structs.Chats{}, chat.ID).Error; err != nil {
		t.Fatalf("delete chat: %v", err)
	}
	if n := countByChat(t, db, &structs.ClassifySegment{}, chat.ID); n != 0 {
		t.Fatalf("升级后的旧库未级联清理 classify_segments，实际残留 %d 行", n)
	}
	if n := countByChat(t, db, &structs.Workflows{}, chat.ID); n != 0 {
		t.Fatalf("升级后的旧库未级联清理 workflows，实际残留 %d 行", n)
	}
	if n := countByChat(t, db, &structs.WorkflowEvents{}, chat.ID); n != 0 {
		t.Fatalf("升级后的旧库未级联清理 workflow_events，实际残留 %d 行", n)
	}
}
