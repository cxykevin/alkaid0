// Package migrate 提供存储层的一次性数据迁移。
//
// 迁移以 metadata 表的「是否存在」作为一次性标记：迁移函数在事务内创建该表并写入
// schema_version；此后即使表中没有版本记录也不再重复执行本迁移，后续 schema 变更
// 应由 metadata 中的版本号驱动。
package migrate

import (
	"fmt"
	"time"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

var logger = log.New("storage:migrate")

// schemaVersionTree 本次迁移完成后的初始 schema 版本号。
const schemaVersionTree = "1"

// metadata 表使用的键名。
const (
	metadataKeySchemaVersion = "schema_version"
	metadataKeyMigratedAt    = "migrated_at"
)

// MigrateToTree 把线性的消息历史迁移为树形结构（为 fork/rewind 打基础）。
//
// 触发条件：当且仅当 metadata 表不存在时执行。metadata 表的存在本身就是
// 「已迁移」的标记——即使表中没有任何版本记录也不再重复执行。
//
// 执行流程（详见各步骤注释）：
//  1. 迁移条件检查（HasTable("metadata")）；
//  2. 日志提示用户备份数据库文件（不自动复制文件）；
//  3. 事务内 AutoMigrate 添加 messages.parent_id、chats.active_leaf_id 与 metadata 表；
//  4. 事务内回填 messages.parent_id（线性历史 → 树：父 = 同会话内上一条消息）；
//  5. 事务内回填 chats.active_leaf_id（各会话最新消息，空会话保持 NULL）；
//  6. 事务内写入 schema_version / migrated_at；
//  7. 迁移后完整性校验与结果日志（回填行数、耗时）。
//
// 错误处理：步骤 3–6 在单个事务内完成，任一步失败即整体回滚——metadata 表随之
// 消失，下次启动会重新尝试迁移。所有回填 SQL 都带「IS NULL」条件，即使 SQLite 的
// DDL 无法完全回滚、出现部分提交，重跑也不会覆盖已回填的数据。
//
// 本次迁移不包含任何 fork/rewind 业务逻辑，也不重命名、删除任何现有列。
func MigrateToTree(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("migrate: nil db handle")
	}

	// 步骤 1：迁移条件检查。metadata 表存在 = 已迁移过（无论其中有没有版本记录）。
	if db.Migrator().HasTable("metadata") {
		logger.Debug("metadata table present, tree migration already applied, skipping")
		return nil
	}

	// 步骤 2：仅提示用户备份，不自动复制数据库文件。
	// 数据库文件路径从 pragma_database_list 读取（Migrator().CurrentDatabase()
	// 返回的是库名 main，拿不到路径）；内存库取不到路径时只给通用提示。
	var dbFile string
	if row := db.Raw("SELECT file FROM pragma_database_list WHERE name = 'main'").Row(); row != nil {
		_ = row.Scan(&dbFile)
	}
	if dbFile != "" {
		logger.Warn("tree migration is about to run on %s; please back up this SQLite database file first (no automatic backup is performed)", dbFile)
	} else {
		logger.Warn("tree migration is about to run; please back up your SQLite database file first (no automatic backup is performed)")
	}

	started := time.Now()
	var messageRows, chatRows int64

	// 步骤 3–6：整个迁移在单个事务内完成。
	err := db.Transaction(func(tx *gorm.DB) error {
		// 步骤 3：添加新列与 metadata 表。SQLite 对可空列的 ALTER TABLE ADD COLUMN
		// 是原生支持、不重建表的；新列都是可空列，旧数据不会丢失。让 GORM 的
		// AutoMigrate 自行判断哪些能直接加列，不手写 ALTER TABLE。
		if err := tx.AutoMigrate(&structs.Messages{}, &structs.Chats{}, &structs.Metadata{}); err != nil {
			return fmt.Errorf("automigrate messages/chats/metadata: %w", err)
		}
		logger.Debug("tree migration: messages.parent_id / chats.active_leaf_id / metadata ready")

		// 步骤 4：回填 messages.parent_id（线性历史 → 树）。
		// 同一 chat 内，id 小于当前行的最大 id 即父节点；每个 chat 中 id 最小的
		// 那条消息的子查询结果为 NULL，保持 NULL 作为根节点。
		// WHERE parent_id IS NULL 保证幂等：重复执行不会覆盖已回填的值。
		res := tx.Exec(`UPDATE messages
SET parent_id = (
    SELECT MAX(m2.id)
    FROM messages m2
    WHERE m2.chat_id = messages.chat_id
      AND m2.id < messages.id
)
WHERE parent_id IS NULL`)
		if res.Error != nil {
			return fmt.Errorf("backfill messages.parent_id: %w", res.Error)
		}
		messageRows = res.RowsAffected
		logger.Debug("tree migration: messages.parent_id backfill touched %d row(s)", messageRows)

		// 步骤 5：回填 chats.active_leaf_id（每个会话的最新消息；空会话保持 NULL）。
		res = tx.Exec(`UPDATE chats
SET active_leaf_id = (
    SELECT MAX(id) FROM messages WHERE messages.chat_id = chats.id
)
WHERE active_leaf_id IS NULL`)
		if res.Error != nil {
			return fmt.Errorf("backfill chats.active_leaf_id: %w", res.Error)
		}
		chatRows = res.RowsAffected
		logger.Debug("tree migration: chats.active_leaf_id backfill touched %d row(s)", chatRows)

		// 步骤 6：写入版本记录（metadata 表刚建好，表内必为空，不存在主键冲突）。
		if err := tx.Create(&structs.Metadata{Key: metadataKeySchemaVersion, Value: schemaVersionTree}).Error; err != nil {
			return fmt.Errorf("write metadata schema_version: %w", err)
		}
		if err := tx.Create(&structs.Metadata{Key: metadataKeyMigratedAt, Value: time.Now().Format(time.RFC3339)}).Error; err != nil {
			return fmt.Errorf("write metadata migrated_at: %w", err)
		}
		return nil
	})
	if err != nil {
		logger.Error("tree migration failed and was rolled back; it will be retried on next startup: %v", err)
		return err
	}

	// 步骤 7：迁移后的完整性校验与结果日志。
	for _, issue := range verifyTree(db) {
		logger.Error("tree migration verification failed: %s", issue)
	}
	logger.Info("tree migration completed: %d message row(s) backfilled, %d chat row(s) backfilled, took %s",
		messageRows, chatRows, time.Since(started).Round(time.Millisecond))
	return nil
}

// verifyTree 执行迁移后的完整性校验，返回所有未通过项的说明（为空 = 全部通过）。
//
// 四组校验（与迁移需求一致）：
//  1. 每个 chat 恰好一个根节点（完全没消息的 chat 不产生分组，属正常）；
//  2. 不存在跨 chat 的父引用；
//  3. 父节点 ID 必须小于子节点 ID；
//  4. chats.active_leaf_id 必须指向真实存在的消息。
func verifyTree(db *gorm.DB) []string {
	var issues []string

	// 校验 1：每个 chat 恰好一个根节点。
	var multiRoot []struct {
		ChatID uint32 `gorm:"column:chat_id"`
		Count  int64  `gorm:"column:c"`
	}
	if err := db.Raw(`SELECT chat_id, COUNT(*) AS c
FROM messages
WHERE parent_id IS NULL
GROUP BY chat_id
HAVING COUNT(*) != 1`).Scan(&multiRoot).Error; err != nil {
		issues = append(issues, fmt.Sprintf("[root-count] check failed to run: %v", err))
	} else if len(multiRoot) > 0 {
		first := multiRoot[0]
		issues = append(issues, fmt.Sprintf("[root-count] chat %d has %d root message(s), expected exactly 1 (%d chat(s) affected in total)",
			first.ChatID, first.Count, len(multiRoot)))
	}

	// 校验 2：没有跨 chat 的父引用。
	var crossIDs []uint64
	if err := db.Raw(`SELECT m.id
FROM messages m
JOIN messages p ON p.id = m.parent_id
WHERE m.chat_id != p.chat_id
LIMIT 1`).Scan(&crossIDs).Error; err != nil {
		issues = append(issues, fmt.Sprintf("[cross-chat-parent] check failed to run: %v", err))
	} else if len(crossIDs) > 0 {
		issues = append(issues, fmt.Sprintf("[cross-chat-parent] message %d references a parent belonging to another chat", crossIDs[0]))
	}

	// 校验 3：父节点 ID 必须小于子节点 ID。
	var backwardIDs []uint64
	if err := db.Raw(`SELECT m.id
FROM messages m
JOIN messages p ON p.id = m.parent_id
WHERE p.id >= m.id
LIMIT 1`).Scan(&backwardIDs).Error; err != nil {
		issues = append(issues, fmt.Sprintf("[parent-order] check failed to run: %v", err))
	} else if len(backwardIDs) > 0 {
		issues = append(issues, fmt.Sprintf("[parent-order] message %d has a parent with id >= its own id", backwardIDs[0]))
	}

	// 校验 4：ActiveLeafID 必须指向真实存在的消息。
	var dangling []uint32
	if err := db.Raw(`SELECT c.id
FROM chats c
LEFT JOIN messages m ON m.id = c.active_leaf_id
WHERE c.active_leaf_id IS NOT NULL AND m.id IS NULL
LIMIT 1`).Scan(&dangling).Error; err != nil {
		issues = append(issues, fmt.Sprintf("[active-leaf] check failed to run: %v", err))
	} else if len(dangling) > 0 {
		issues = append(issues, fmt.Sprintf("[active-leaf] chat %d active_leaf_id points to a missing message", dangling[0]))
	}

	return issues
}
