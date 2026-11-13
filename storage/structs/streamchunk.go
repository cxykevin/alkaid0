package structs

import (
	"errors"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// StreamChunk 是流式响应正文/思考的追加式日志，只为「进程被异常终止后仍能还原
// 最后一段内容」而存在：流式过程中每累积到阈值就把**新增片段**插入一行，
// 消息行的 delta / thinking_delta 列留到收尾时一次性写入。
//
// 为什么不再「每 256 字符把整段内容 UPDATE 回消息行」：内容列每次 UPDATE 都要
// 整行重写，长度 n 的响应总写入量是 O(n²/阈值)——700 KiB 响应约 1 GB 级行重写，
// 外加同量 WAL；而追加日志的总写入量只有 O(n)（等于正文本身）。旧的整列刷写还
// 会让流式过程中每 256 字符在 Go 侧多一次整段字符串拼接/比较。
//
// 生命周期：
//   - 流式中：StreamJournal.Write 按阈值写 stream_chunks 行；
//   - 收尾：FinalizeStreamMessage 在单事务内写入最终内容并删除日志行；
//   - 被强杀：日志行残留，下次打开数据库时由 RecoverStreamChunks 合并回消息行
//     （见 storage.InitDB）。
type StreamChunk struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	MessageID uint64 `gorm:"index:idx_stream_chunk_message"`
	// Thinking 区分思考流（thinking_delta）与正文流（delta）。两者在同一个流里
	// 交错到达，各自按插入顺序（ID 升序）拼接即可还原原文。
	Thinking bool
	Content  string `gorm:"type:text"`
}

// streamChunkFlushThreshold 是追加日志的默认刷写阈值（字符），与旧的整列刷写
// 阈值保持一致：被强杀时最多丢失最后一个不足阈值的片段。
const streamChunkFlushThreshold = 256

// streamMessageKey 是活跃流式消息的登记键。
//
// 消息 ID 只在单个数据库内唯一（每个工作目录一份 db.sqlite），所以必须带上数据库
// 句柄：否则 A 目录里正在生成的消息 42 会让 B 目录里遗留的消息 42 永远不被恢复。
// server/actions 的 loadDB 按工作目录缓存 *gorm.DB，同一目录的会话共享同一个句柄，
// 因此句柄相等就等价于「同一个数据库」。
type streamMessageKey struct {
	db        *gorm.DB
	messageID uint64
}

// activeStreamMessages 记录本进程里仍在进行的流式消息。RecoverStreamChunks 必须
// 跳过它们：此时把日志合并进消息行会让日志提前消失，而这条流还要继续追加，
// 万一中途再被强杀，已经合并的部分就真的没有第二份了。同一个数据库在本进程内
// 可能被再次打开（会话释放后重新加载），因此不能假设"恢复只发生在启动时"。
var activeStreamMessages sync.Map // map[streamMessageKey]struct{}

func registerStreamMessage(db *gorm.DB, messageID uint64) {
	activeStreamMessages.Store(streamMessageKey{db: db, messageID: messageID}, struct{}{})
}

func unregisterStreamMessage(db *gorm.DB, messageID uint64) {
	activeStreamMessages.Delete(streamMessageKey{db: db, messageID: messageID})
}

// IsStreamMessageActive 报告该库里的这条消息是否由本进程某个尚未收尾的流持有。
func IsStreamMessageActive(db *gorm.DB, messageID uint64) bool {
	_, ok := activeStreamMessages.Load(streamMessageKey{db: db, messageID: messageID})
	return ok
}

// StreamJournal 把一条流式响应的增量按阈值追加到 stream_chunks。
//
// 调用方负责在内存里累积完整正文（收尾时一次性写入），Journal 只负责记录
// 「已收到哪些新增片段」这一崩溃恢复日志。零值不可用，必须经 NewStreamJournal
// 创建（它同时登记活跃消息，避免崩溃恢复误伤进行中的流）。
type StreamJournal struct {
	db        *gorm.DB
	messageID uint64
	threshold int

	pendingDelta    strings.Builder
	pendingThinking strings.Builder
}

// NewStreamJournal 为 msgID 创建追加日志；threshold <= 0 时用默认阈值。
func NewStreamJournal(db *gorm.DB, messageID uint64, threshold int) *StreamJournal {
	if threshold <= 0 {
		threshold = streamChunkFlushThreshold
	}
	registerStreamMessage(db, messageID)
	return &StreamJournal{db: db, messageID: messageID, threshold: threshold}
}

// Pending 返回尚未落库的增量长度（字符）。
func (j *StreamJournal) Pending() int {
	if j == nil {
		return 0
	}
	return j.pendingDelta.Len() + j.pendingThinking.Len()
}

// Write 记录一段增量；未达阈值时只在内存里攒着，达到阈值才写库。
func (j *StreamJournal) Write(delta, thinking string) error {
	if j == nil {
		return nil
	}
	j.pendingDelta.WriteString(delta)
	j.pendingThinking.WriteString(thinking)
	if j.Pending() < j.threshold {
		return nil
	}
	return j.Flush()
}

// Flush 把内存里的增量写入日志表；没有增量时什么也不做。
// 写库失败时内存里的增量保留，调用方可以重试或直接收尾（收尾写的是完整内容）。
func (j *StreamJournal) Flush() error {
	if j == nil || j.db == nil || j.messageID == 0 {
		return nil
	}
	delta := j.pendingDelta.String()
	thinking := j.pendingThinking.String()
	if delta == "" && thinking == "" {
		return nil
	}
	rows := make([]StreamChunk, 0, 2)
	if delta != "" {
		rows = append(rows, StreamChunk{MessageID: j.messageID, Content: delta})
	}
	if thinking != "" {
		rows = append(rows, StreamChunk{MessageID: j.messageID, Thinking: true, Content: thinking})
	}
	// 两种片段放在同一个事务里：被强杀时不会出现「正文这一段在、思考那一段丢」
	// 的半截状态（最多丢最后一个不足阈值的片段）。
	if err := j.db.Transaction(func(tx *gorm.DB) error {
		return tx.Create(&rows).Error
	}); err != nil {
		return err
	}
	j.pendingDelta.Reset()
	j.pendingThinking.Reset()
	return nil
}

// Close 注销活跃登记（收尾后调用）。重复调用安全。
func (j *StreamJournal) Close() {
	if j == nil || j.messageID == 0 {
		return
	}
	unregisterStreamMessage(j.db, j.messageID)
}

// StreamFinal 是流式响应收尾时写入消息行的最终内容。
type StreamFinal struct {
	Delta            string
	ThinkingDelta    string
	Phase            string
	PromptTokens     uint32
	CompletionTokens uint32
	TotalTokens      uint32
	CachedTokens     uint32
}

// FinalizeStreamMessage 在单个事务内收尾一条流式消息：写入最终正文/思考/phase/
// token 用量，并删除该消息的追加日志。
//
// 用 map 而不是结构体更新是刻意的：收尾只发生一次，所有列都要落到最终值（包括
// 最终为空），而 GORM 的结构体更新会跳过零值列——正文为空但 phase 有值的场景
// （纯工具调用轮）会因此留下过期的旧值。
func FinalizeStreamMessage(db *gorm.DB, messageID uint64, final *StreamFinal) error {
	if db == nil || messageID == 0 {
		return errors.New("structs: FinalizeStreamMessage: invalid db or message id")
	}
	if final == nil {
		return errors.New("structs: FinalizeStreamMessage: nil final content")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Messages{}).Where("id = ?", messageID).Updates(map[string]any{
			"delta":             final.Delta,
			"thinking_delta":    final.ThinkingDelta,
			"phase":             final.Phase,
			"prompt_tokens":     final.PromptTokens,
			"completion_tokens": final.CompletionTokens,
			"total_tokens":      final.TotalTokens,
			"cached_tokens":     final.CachedTokens,
		}).Error; err != nil {
			return err
		}
		return tx.Where("message_id = ?", messageID).Delete(&StreamChunk{}).Error
	})
}

// RecoverStreamChunks 把异常退出时残留的追加日志合并回消息行的 delta /
// thinking_delta，并删除这些日志行。返回合并的消息条数。
//
// 正常收尾的消息不会有日志行（收尾事务已删除），因此这是个几乎零成本的查询；
// 本进程仍在进行的流式消息被跳过。幂等：合并与删除在同一事务内完成，重复调用
// 只会处理新残留。错误向上返回（调用方只记日志，下次打开数据库重试）。
func RecoverStreamChunks(db *gorm.DB) (int, error) {
	if db == nil {
		return 0, nil
	}
	if !db.Migrator().HasTable(&StreamChunk{}) {
		return 0, nil
	}
	var rows []StreamChunk
	// 单条 SQL 取全部残留：崩溃残留通常只有几条到几千条（单条响应一个片段一行），
	// 一次读出来比按 message_id 逐条查更省事。
	if err := db.Order("message_id ASC, id ASC").Find(&rows).Error; err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	recovered := 0
	for start := 0; start < len(rows); {
		messageID := rows[start].MessageID
		var delta, thinking strings.Builder
		end := start
		for ; end < len(rows) && rows[end].MessageID == messageID; end++ {
			if rows[end].Thinking {
				thinking.WriteString(rows[end].Content)
			} else {
				delta.WriteString(rows[end].Content)
			}
		}
		start = end

		if IsStreamMessageActive(db, messageID) {
			continue
		}
		if err := mergeStreamChunks(db, messageID, delta.String(), thinking.String()); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

// mergeStreamChunks 把一条消息的残留日志追加到消息行并删除日志（单事务）。
func mergeStreamChunks(db *gorm.DB, messageID uint64, delta, thinking string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// 用 Find 而非 First：消息可能已被删除（占位行清理 / 会话被删），
		// 此时日志失去意义，直接丢弃。
		var msgs []Messages
		if err := tx.Select("id", "delta", "thinking_delta").
			Where("id = ?", messageID).Limit(1).Find(&msgs).Error; err != nil {
			return err
		}
		if len(msgs) > 0 {
			updates := make(map[string]any, 2)
			if delta != "" {
				updates["delta"] = msgs[0].Delta + delta
			}
			if thinking != "" {
				updates["thinking_delta"] = msgs[0].ThinkingDelta + thinking
			}
			if len(updates) > 0 {
				if err := tx.Model(&Messages{}).Where("id = ?", messageID).
					Updates(updates).Error; err != nil {
					return err
				}
			}
		}
		return tx.Where("message_id = ?", messageID).Delete(&StreamChunk{}).Error
	})
}
