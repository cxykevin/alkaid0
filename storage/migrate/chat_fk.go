package migrate

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// 本文件实现「解除子表对 chats 的外键绑定」一次性迁移。
//
// 设计背景：deleteChat 只删会话表、保留消息等聊天记录（见 ui/funcs.DeleteChat），
// 因此任何子表都不应再对 chats 声明外键——外键要么阻止会话行删除
// （OnDelete:RESTRICT / NO ACTION），要么把子表数据级联删除（CASCADE），两者都与
// 「只删会话表」的设计冲突。子表模型已不再声明 Chats 关联（全新库不生成外键），
// 但历史库中已有的外键不会被 AutoMigrate 移除（GORM 只补建约束、从不删除），
// 需要本迁移把带外键的表逐张重建一遍。

// schemaVersionRemoveChatFK 本迁移完成后的 schema 版本号。
// MigrateToTree 写入 "1"，本迁移提升到 "2"（版本号机制见 migrate.go 包注释）。
const schemaVersionRemoveChatFK = "2"

// chatFKTmpSuffix 重建过程中旧表数据转存用的临时表名后缀。
const chatFKTmpSuffix = "__chat_fk_removal_old"

// chatChildTable 一张会（或曾经会）对 chats 声明外键的子表：表名 + 当前模型。
type chatChildTable struct {
	table string
	model any
}

// chatChildTables 需要解绑 chats 外键的全部子表（与各 struct 中的「设计注记」对应）。
// 迁移对每张表先检查是否真的还带外键，只重建确实需要的表；未来新增子表无需在此
// 追加——新表由当前模型直接建出，本来就没有外键。
var chatChildTables = []chatChildTable{
	{"messages", &structs.Messages{}},
	{"refer_files", &structs.ReferFiles{}},
	{"scopes", &structs.Scopes{}},
	{"terminals", &structs.Terminals{}},
	{"traces", &structs.Traces{}},
	{"workflows", &structs.Workflows{}},
	{"workflow_events", &structs.WorkflowEvents{}},
	{"classify_segments", &structs.ClassifySegment{}},
}

// MigrateRemoveChatForeignKeys 解除全部子表对 chats 的外键绑定。
//
// 触发条件：metadata.schema_version < 2；或版本已达 2 但仍有子表残留外键
// （自愈：正常不会出现——版本号由成功的迁移写入；该兜底检查每个库只花几条
// pragma 查询，换来对被外部改脏的库自动修复，避免会话删除被静默卡住）。
// metadata 表缺失（树迁移未完成）时无法读写版本号，核心工作仍会执行——
// 重建本身幂等，版本记录留待下次启动补写。
//
// 执行流程：
//  1. 版本检查（见上）；
//  2. 事务内扫描全部子表，挑出仍带「引用 chats」外键的表；
//  3. 对每张表重建（rebuildTableWithoutChatFK）：数据转存临时表 → 删除旧表
//     （旧外键与旧索引随旧表一起释放）→ 由当前模型 AutoMigrate 重建
//     （无外键、索引齐备）→ 按列交集回填数据 → 校验行数 → 恢复 AUTOINCREMENT
//     序号 → 校验外键已不存在；
//  4. 事务内把 schema_version 提升为 2。
//
// 错误处理：步骤 2–4 在单个事务内完成，任一步失败整体回滚（表结构与版本号
// 保持原样），下次启动会重新尝试。
func MigrateRemoveChatForeignKeys(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate: nil db handle")
	}

	// 步骤 1：版本检查。
	version, err := readSchemaVersion(db)
	if err != nil {
		return err
	}
	if version >= 2 {
		left, err := anyTableHasChatFK(db)
		if err != nil {
			return err
		}
		if !left {
			logger.Debug("schema_version=%d, chat FK removal already applied, skipping", version)
			return nil
		}
		logger.Warn("schema_version=%d but some tables still carry a chats foreign key; rebuilding again", version)
	}

	started := time.Now()
	var rebuilt []string

	// 步骤 2–4：单个事务内完成扫描、重建与版本写入。
	err = db.Transaction(func(tx *gorm.DB) error {
		pending, err := pendingChatFKTables(tx)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			tables := make([]string, 0, len(pending))
			for _, child := range pending {
				tables = append(tables, child.table)
			}
			// 仅提示用户备份，不自动复制数据库文件（与 MigrateToTree 一致）。
			var dbFile string
			if row := tx.Raw("SELECT file FROM pragma_database_list WHERE name = 'main'").Row(); row != nil {
				_ = row.Scan(&dbFile)
			}
			if dbFile != "" {
				logger.Warn("chat FK removal is about to rebuild %d table(s) (%s) on %s; please back up this SQLite database file first (no automatic backup is performed)",
					len(pending), strings.Join(tables, ", "), dbFile)
			} else {
				logger.Warn("chat FK removal is about to rebuild %d table(s) (%s); please back up your SQLite database file first (no automatic backup is performed)",
					len(pending), strings.Join(tables, ", "))
			}
			for _, child := range pending {
				if err := rebuildTableWithoutChatFK(tx, child.table, child.model); err != nil {
					return err
				}
				rebuilt = append(rebuilt, child.table)
			}
		}
		return writeSchemaVersion(tx, schemaVersionRemoveChatFK)
	})
	if err != nil {
		logger.Error("chat FK removal failed and was rolled back; it will be retried on next startup: %v", err)
		return err
	}

	if len(rebuilt) > 0 {
		logger.Info("chat FK removal completed: %d table(s) rebuilt (%s), took %s",
			len(rebuilt), strings.Join(rebuilt, ", "), time.Since(started).Round(time.Millisecond))
	} else {
		logger.Debug("chat FK removal: no chats foreign key to remove")
	}
	return nil
}

// readSchemaVersion 读取 metadata.schema_version。
// metadata 表、版本记录缺失或值不可解析时均返回 0（视为尚未迁移——迁移本身
// 幂等，重复执行只会快速跳过）。
func readSchemaVersion(db *gorm.DB) (int, error) {
	if !db.Migrator().HasTable("metadata") {
		return 0, nil
	}
	var rows []struct {
		Value string `gorm:"column:value"`
	}
	if err := db.Raw("SELECT value FROM metadata WHERE key = ?", metadataKeySchemaVersion).Scan(&rows).Error; err != nil {
		return 0, fmt.Errorf("read metadata.schema_version: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(rows[0].Value))
	if err != nil {
		logger.Warn("metadata.schema_version = %q is not a number, treating as 0", rows[0].Value)
		return 0, nil
	}
	return v, nil
}

// writeSchemaVersion 把 metadata.schema_version 提升到 v。
// metadata 表缺失（树迁移尚未完成、版本机制未建立）时不做任何事——核心工作
// 已经完成，版本记录留待下次启动（树迁移成功后）补写。
func writeSchemaVersion(db *gorm.DB, v string) error {
	if !db.Migrator().HasTable("metadata") {
		logger.Debug("metadata table missing, chat FK removal version not recorded (will be re-checked on next startup)")
		return nil
	}
	res := db.Model(&structs.Metadata{}).Where("key = ?", metadataKeySchemaVersion).Update("value", v)
	if res.Error != nil {
		return fmt.Errorf("update metadata.schema_version: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		if err := db.Create(&structs.Metadata{Key: metadataKeySchemaVersion, Value: v}).Error; err != nil {
			return fmt.Errorf("insert metadata.schema_version: %w", err)
		}
	}
	return nil
}

// anyTableHasChatFK 快速检查是否仍有子表带着指向 chats 的外键。
func anyTableHasChatFK(db *gorm.DB) (bool, error) {
	for _, child := range chatChildTables {
		if !db.Migrator().HasTable(child.table) {
			continue
		}
		has, err := hasForeignKeyTo(db, child.table, "chats")
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	return false, nil
}

// pendingChatFKTables 挑出仍带着「引用 chats」外键、需要重建的子表。
func pendingChatFKTables(db *gorm.DB) ([]chatChildTable, error) {
	var pending []chatChildTable
	for _, child := range chatChildTables {
		if !db.Migrator().HasTable(child.table) {
			continue
		}
		has, err := hasForeignKeyTo(db, child.table, "chats")
		if err != nil {
			return nil, err
		}
		if has {
			pending = append(pending, child)
		}
	}
	return pending, nil
}

// hasForeignKeyTo 检查 table 是否声明了指向 target 的外键。
func hasForeignKeyTo(db *gorm.DB, table, target string) (bool, error) {
	var n int64
	// table 来自 chatChildTables 白名单常量，直接拼进 pragma 函数参数
	// （pragma 表值函数的表名不支持 ? 绑定）。
	query := fmt.Sprintf(`SELECT count(*) FROM pragma_foreign_key_list('%s') WHERE "table" = ?`, table)
	if err := db.Raw(query, target).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("inspect foreign keys of %s: %w", table, err)
	}
	return n > 0, nil
}

// rebuildTableWithoutChatFK 重建单张子表：去掉对 chats 的外键，保留全部数据与索引。
//
// 流程（在调用方事务内执行）：
//  1. 记录旧行数与 AUTOINCREMENT 序号（sqlite_sequence）；
//  2. CREATE TABLE <tmp> AS SELECT * FROM <table>：数据转存到无约束的临时表；
//  3. DROP TABLE <table>：旧表的外键、索引随表一起释放；
//  4. AutoMigrate(模型)：按当前定义重建表（模型不含 Chats 关联 → 无外键；
//     索引按字段上的 index 标签重建）；
//  5. 按「旧表 ∩ 新表」列交集回填数据（老库可能缺新列，用交集保证两边都可用）；
//  6. 校验行数一致后删除临时表；
//  7. 恢复 AUTOINCREMENT 序号：拷贝旧值（取旧值与当前值的较大者），避免重建
//     导致 rowid 复用——消息 id 等被长期外部引用，绝不可复用；
//  8. 校验外键已不存在、行数一致。
func rebuildTableWithoutChatFK(tx *gorm.DB, table string, model any) error {
	tmp := table + chatFKTmpSuffix

	rowsBefore, err := countTableRows(tx, table)
	if err != nil {
		return err
	}
	seqBefore, hasSeq, err := readAutoIncrementSeq(tx, table)
	if err != nil {
		return err
	}

	// 防御：清掉可能残留的同名临时表（正常仅事务中断后回滚的痕迹，理论上不存在）。
	if err := tx.Exec("DROP TABLE IF EXISTS `" + tmp + "`").Error; err != nil {
		return fmt.Errorf("drop stale temp table %s: %w", tmp, err)
	}
	if err := tx.Exec("CREATE TABLE `" + tmp + "` AS SELECT * FROM `" + table + "`").Error; err != nil {
		return fmt.Errorf("snapshot %s to %s: %w", table, tmp, err)
	}
	if err := tx.Exec("DROP TABLE `" + table + "`").Error; err != nil {
		return fmt.Errorf("drop old table %s: %w", table, err)
	}
	if err := tx.AutoMigrate(model); err != nil {
		return fmt.Errorf("recreate table %s: %w", table, err)
	}
	cols, err := commonColumns(tx, tmp, table)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return fmt.Errorf("recreate table %s: no common columns with its snapshot", table)
	}
	quoted := "`" + strings.Join(cols, "`,`") + "`"
	if err := tx.Exec("INSERT INTO `" + table + "` (" + quoted + ") SELECT " + quoted + " FROM `" + tmp + "`").Error; err != nil {
		return fmt.Errorf("restore %s data: %w", table, err)
	}
	rowsAfter, err := countTableRows(tx, table)
	if err != nil {
		return err
	}
	if rowsAfter != rowsBefore {
		return fmt.Errorf("table %s row count mismatch after rebuild: %d -> %d", table, rowsBefore, rowsAfter)
	}
	if err := tx.Exec("DROP TABLE `" + tmp + "`").Error; err != nil {
		return fmt.Errorf("drop temp table %s: %w", tmp, err)
	}
	if hasSeq {
		if err := restoreAutoIncrementSeq(tx, table, seqBefore); err != nil {
			return err
		}
	}
	has, err := hasForeignKeyTo(tx, table, "chats")
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("table %s still holds a chats foreign key after rebuild", table)
	}
	logger.Debug("chat FK removal: rebuilt %s (%d row(s) restored)", table, rowsAfter)
	return nil
}

// countTableRows 统计表行数（表名来自白名单常量）。
func countTableRows(db *gorm.DB, table string) (int64, error) {
	var n int64
	if err := db.Raw("SELECT count(*) FROM `" + table + "`").Scan(&n).Error; err != nil {
		return 0, fmt.Errorf("count %s: %w", table, err)
	}
	return n, nil
}

// commonColumns 返回「新表列序 ∩ 转存表列」的列名列表（用于数据回填）。
func commonColumns(db *gorm.DB, snapshot, recreated string) ([]string, error) {
	oldCols, err := tableColumns(db, snapshot)
	if err != nil {
		return nil, err
	}
	newCols, err := tableColumns(db, recreated)
	if err != nil {
		return nil, err
	}
	oldSet := make(map[string]bool, len(oldCols))
	for _, c := range oldCols {
		oldSet[c] = true
	}
	var cols []string
	for _, c := range newCols {
		if oldSet[c] {
			cols = append(cols, c)
		}
	}
	return cols, nil
}

// tableColumns 返回表的列名（按定义顺序，表名来自白名单常量）。
func tableColumns(db *gorm.DB, table string) ([]string, error) {
	var rows []struct {
		Name string `gorm:"column:name"`
	}
	query := fmt.Sprintf(`SELECT name FROM pragma_table_info('%s')`, table)
	if err := db.Raw(query).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("read columns of %s: %w", table, err)
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return names, nil
}

// readAutoIncrementSeq 读取表在 sqlite_sequence 中记录的 AUTOINCREMENT 序号。
// has=false 表示无序号记录（表不是 AUTOINCREMENT，或从未插入过数据）。
func readAutoIncrementSeq(db *gorm.DB, table string) (int64, bool, error) {
	if !db.Migrator().HasTable("sqlite_sequence") {
		return 0, false, nil
	}
	var rows []struct {
		Seq int64 `gorm:"column:seq"`
	}
	if err := db.Raw("SELECT seq FROM sqlite_sequence WHERE name = ?", table).Scan(&rows).Error; err != nil {
		return 0, false, fmt.Errorf("read sqlite_sequence[%s]: %w", table, err)
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return rows[0].Seq, true, nil
}

// restoreAutoIncrementSeq 恢复表重建前的 AUTOINCREMENT 序号
// （取旧值与当前值的较大者）。
func restoreAutoIncrementSeq(db *gorm.DB, table string, seqBefore int64) error {
	current, hasCurrent, err := readAutoIncrementSeq(db, table)
	if err != nil {
		return err
	}
	target := seqBefore
	if hasCurrent && current > target {
		target = current
	}
	res := db.Exec("UPDATE sqlite_sequence SET seq = ? WHERE name = ?", target, table)
	if res.Error != nil {
		return fmt.Errorf("restore sqlite_sequence[%s]: %w", table, res.Error)
	}
	if res.RowsAffected == 0 {
		if err := db.Exec("INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)", table, target).Error; err != nil {
			return fmt.Errorf("insert sqlite_sequence[%s]: %w", table, err)
		}
	}
	return nil
}
