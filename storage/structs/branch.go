package structs

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// 本文件实现消息树（活跃分支）的读取与维护。
//
// 消息表自 v0.5.x 起为树形结构（见 storage/migrate.MigrateToTree）：
//   - messages.parent_id   指向消息的父消息，NULL 表示它是所在分支的根（会话第一条消息）；
//   - chats.active_leaf_id 指向会话当前活跃分支的叶子（该分支最新一条消息）。
//
// 「活跃分支」= 从 active_leaf_id 出发、沿 parent_id 一直回溯到根的祖先链。
// 线性历史只是树的一个特例（每个节点最多一个子节点）；fork/rewind 让一条会话
// 中可以同时存在多条分支，只有活跃分支参与构建请求、回放、压缩与总结——
// 因此所有对消息历史的遍历都必须经 OnActiveBranch 限定。
//
// 兼容回退：若会话的 active_leaf_id 为空（空会话、回填未完成的历史库、
// 测试中直接 Create 的线性消息），OnActiveBranch 退化为按 chat_id 的全量扫描，
// 行为与迁移前的线性读取完全一致。

// activeBranchCondition 是「限定活跃分支」的 WHERE 片段：
//
//	((chat_id = ? AND (SELECT active_leaf_id FROM chats WHERE id = ?) IS NULL)
//	 OR id IN (WITH RECURSIVE branch(id, parent_id) AS (
//	     SELECT id, parent_id FROM messages WHERE id = (SELECT active_leaf_id FROM chats WHERE id = ?)
//	     UNION ALL
//	     SELECT m.id, m.parent_id FROM messages m JOIN branch b ON m.id = b.parent_id
//	 ) SELECT id FROM branch))
//
// 外层大括号保证整个表达式作为单个条件与调用方的其它条件用 AND 组合
// （SQL 中 AND 优先级高于 OR，若不加会把调用方条件卷入 OR 分支）。
// 三个占位符都传 chatID；chats 行不存在时子查询为 NULL，同样走回退分支。
const activeBranchCondition = `((chat_id = ? AND (SELECT active_leaf_id FROM chats WHERE id = ?) IS NULL) OR id IN (` +
	`WITH RECURSIVE branch(id, parent_id) AS (` +
	`SELECT id, parent_id FROM messages WHERE id = (SELECT active_leaf_id FROM chats WHERE id = ?) ` +
	`UNION ALL ` +
	`SELECT m.id, m.parent_id FROM messages m JOIN branch b ON m.id = b.parent_id` +
	`) SELECT id FROM branch))`

// OnActiveBranch 把 messages 查询限定在会话当前的活跃分支上。
// 返回的 *gorm.DB 可继续链式 .Where / .Order / .Offset / .Count / .Find 等。
func OnActiveBranch(db *gorm.DB, chatID uint32) *gorm.DB {
	return db.Where(activeBranchCondition, chatID, chatID, chatID)
}

// localBackfillSQL 把一个会话内所有未挂父节点的消息按 id 串成链，
// 与全量迁移（storage/migrate.MigrateToTree 的回填步骤）规则一致：
// 父 = 同会话内 id 小于当前行的最大 id；最小 id 的行保持 NULL（根节点）。
// 仅在「会话已有消息但 active_leaf_id 为空」的异常状态下，由 localBackfillLeaf
// （AppendMessage / RewindTo 共用）触发。
const localBackfillSQL = `UPDATE messages
SET parent_id = (
    SELECT MAX(m2.id)
    FROM messages m2
    WHERE m2.chat_id = messages.chat_id
      AND m2.id < messages.id
)
WHERE chat_id = ? AND parent_id IS NULL`

// localBackfillLeaf 处理「会话已有历史消息、但 active_leaf_id 为空」的异常状态
// （迁移未完成、数据由旧代码/测试直接写入等）：把未挂父节点的消息按 id 局部
// 回填成链，并返回链尾（最新一条消息）的 id。会话没有消息时返回 nil——空会话
// 属正常情况，用 Find 而非 First，不应产生 record not found 日志。
// AppendMessage 与 RewindTo 共用该修复，保证两者对异常状态的处理口径一致。
func localBackfillLeaf(tx *gorm.DB, chatID uint32) (*uint64, error) {
	var latest []Messages
	if err := tx.Select("id").
		Where("chat_id = ?", chatID).
		Order("id DESC").Limit(1).Find(&latest).Error; err != nil {
		return nil, err
	}
	if len(latest) == 0 {
		return nil, nil
	}
	if err := tx.Exec(localBackfillSQL, chatID).Error; err != nil {
		return nil, err
	}
	return &latest[0].ID, nil
}

// AppendMessage 把消息追加到会话活跃分支的末端，并维护树的两端：
//   - msg.ParentID 置为会话当前的 active_leaf_id（空会话为 NULL，即新根节点）；
//   - 插入成功后把 chats.active_leaf_id 推进到这条新消息。
//
// 一致性修复：若会话已有历史消息但没有叶子（如迁移未完成、或数据由旧代码/
// 测试直接写入），先经 localBackfillLeaf 把消息按 id 局部回填成链、以最新一条
// 为叶子，再挂载新消息——保证新写入不会让已有历史在活跃分支上"消失"。
//
// chats 行不存在时（部分测试直接建消息的场景）跳过树维护，行为与旧代码一致。
// 整个过程位于单个事务内；SQLite 连接池单连接串行，读-改-写不会交错。
func AppendMessage(db *gorm.DB, msg *Messages) error {
	if db == nil || msg == nil {
		return errors.New("structs: AppendMessage: nil db or message")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var chat Chats
		err := tx.Select("id", "active_leaf_id").First(&chat, msg.ChatID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Create(msg).Error
		}
		if err != nil {
			return err
		}

		leaf := chat.ActiveLeafID
		if leaf == nil {
			leaf, err = localBackfillLeaf(tx, msg.ChatID)
			if err != nil {
				return err
			}
		}

		msg.ParentID = leaf
		if err := tx.Create(msg).Error; err != nil {
			return err
		}
		return tx.Model(&Chats{}).Where("id = ?", msg.ChatID).
			Update("active_leaf_id", msg.ID).Error
	})
}

// DeleteMessage 删除一条消息并修补消息树：
//   - 它的直接子节点（若有）改挂到它的父节点——删除中间节点时链保持连续；
//   - 若被删除的正是会话的 active_leaf_id，叶子回退到它的父节点。
//
// 消息已不存在时视为已完成（幂等，容忍重复删除）。整个过程位于单个事务内。
func DeleteMessage(db *gorm.DB, msgID uint64) error {
	if db == nil || msgID == 0 {
		return errors.New("structs: DeleteMessage: invalid db or message id")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		// 用 Find 而非 First：消息已不存在（重复删除）属正常容忍场景
		var msgs []Messages
		if err := tx.Select("id", "chat_id", "parent_id").
			Where("id = ?", msgID).Limit(1).Find(&msgs).Error; err != nil {
			return err
		}
		if len(msgs) == 0 {
			return nil
		}
		msg := &msgs[0]
		if err := tx.Delete(&Messages{}, msgID).Error; err != nil {
			return err
		}
		if err := tx.Model(&Messages{}).Where("parent_id = ?", msgID).
			Update("parent_id", nullableID(msg.ParentID)).Error; err != nil {
			return err
		}
		return tx.Model(&Chats{}).
			Where("id = ? AND active_leaf_id = ?", msg.ChatID, msgID).
			Update("active_leaf_id", nullableID(msg.ParentID)).Error
	})
}

// RewindTo 把会话头（chats.active_leaf_id）移动到同一会话内指定的历史消息，
// 即把「活跃分支」收窄到该消息的祖先链。rewind 不删除任何数据：
//   - 目标消息之后的原分支消息保留在库中成为旁支，之后可再次 RewindTo 回到
//     原来的位置；
//   - 目标可以是本会话的任意一条消息（含旁支消息）；后续 AppendMessage 会以
//     新的会话头为父节点继续追加，从而形成分叉。
//
// 校验：会话必须存在；目标消息必须存在且属于该会话。否则报错且不产生任何
// 改动（整个操作位于单个事务内）。目标已是当前会话头时视为幂等成功。
//
// 会话身份不变：本操作不新建、不删除会话行，也不改变任何消息 id；SessionID
// （server 侧由 Chats.ID 与工作目录派生，形如 sess_<id>:<cwd>）因此保持不变，
// 客户端持有的 sessionId 与已知 messageId 在 rewind 前后继续有效。
//
// 一致性修复（与 AppendMessage 共用 localBackfillLeaf）：若会话已有历史消息
// 但没有叶子（异常状态），先把未挂父节点的消息局部回填成链再移动会话头——
// 保证早于目标的既有历史不会因 rewind 而从活跃分支上"消失"。
func RewindTo(db *gorm.DB, chatID uint32, msgID uint64) error {
	if db == nil || chatID == 0 || msgID == 0 {
		return errors.New("structs: RewindTo: invalid db or ids")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var chat Chats
		if err := tx.Select("id", "active_leaf_id").First(&chat, chatID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("structs: RewindTo: chat %d not found", chatID)
			}
			return err
		}
		var msg Messages
		if err := tx.Select("id", "chat_id").First(&msg, msgID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("structs: RewindTo: message %d not found", msgID)
			}
			return err
		}
		if msg.ChatID != chatID {
			return fmt.Errorf("structs: RewindTo: message %d does not belong to chat %d", msgID, chatID)
		}
		if chat.ActiveLeafID != nil && *chat.ActiveLeafID == msgID {
			// 已在目标位置：幂等成功
			return nil
		}
		if chat.ActiveLeafID == nil {
			// 异常状态（有消息、无叶子）：先补链再移动，保证既有历史不消失
			if _, err := localBackfillLeaf(tx, chatID); err != nil {
				return err
			}
		}
		return tx.Model(&Chats{}).Where("id = ?", chatID).
			Update("active_leaf_id", msgID).Error
	})
}

// nullableID 把 *uint64 主键转换为可直接作为 SQL 参数的值：
// nil -> NULL，非 nil -> 基础类型值（database/sql 不支持 *uint64 指针转换）。
func nullableID(id *uint64) any {
	if id == nil {
		return nil
	}
	return *id
}
