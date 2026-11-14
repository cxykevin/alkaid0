package structs

import (
	"gorm.io/gorm"
)

// KeyMapping 记录「原敏感值 → 脱敏假值」的映射，用于请求出站脱敏与响应还原。
// 表存放在项目级 db.sqlite（每 cwd 一个数据库），因此映射天然按项目隔离。
// Original 唯一保证同一个 key 始终映射到同一个假值；Masked 唯一保证单射、还原无歧义。
//
// 淘汰（见 provider/mask/evict.go）：只有「原值已不在发送窗口里」的行才允许删除，
// 否则同一个原值会被重新脱敏成另一个假值，破坏 provider 的 prompt prefix cache。
// 判断依据就是 LastUsed —— 出站脱敏每命中一次就刷新，而整份 prompt 每轮都会重新
// 脱敏，所以仍会被发送的原值永远处于"热"状态。
type KeyMapping struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement"`
	KeyType  string `gorm:"size:32;index:idx_type_orig,unique"` // apikey | phone | ip | session | cookie | jwt | custom
	Original string `gorm:"type:text;index:idx_type_orig,unique"`
	Masked   string `gorm:"type:text;uniqueIndex"`
	Time     uint64 `gorm:"autoCreateTime"`
	// LastUsed 最后一次被出站脱敏命中的 unix 秒（创建时即等于当前时间）。
	// 淘汰只按它判断：陈旧到超过 prompt 缓存保留期的行才会被删。
	LastUsed uint64 `gorm:"index:idx_km_last_used"`
	// Hits 累计命中次数（出站脱敏方向）。仅用于观测与日志，不参与淘汰决策。
	Hits uint32
}

// BackfillKeyMappingLastUsed 把历史行的 last_used 回填为创建时间（`time` 列）。
//
// 新列默认 0；若原样参与淘汰，所有历史行都会被当成"从远古起就没用过"而在第一次
// 清理时被整表删掉。只回填 last_used = 0 的行，因此幂等，也不会每次开库全表写。
func BackfillKeyMappingLastUsed(db *gorm.DB) (int64, error) {
	if db == nil || !db.Migrator().HasTable(&KeyMapping{}) {
		return 0, nil
	}
	res := db.Model(&KeyMapping{}).Where("last_used = 0").Update("last_used", gorm.Expr("`time`"))
	return res.RowsAffected, res.Error
}
