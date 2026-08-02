package structs

// KeyMapping 记录「原敏感值 → 脱敏假值」的映射，用于请求出站脱敏与响应还原。
// 表存放在项目级 db.sqlite（每 cwd 一个数据库），因此映射天然按项目隔离。
// Original 唯一保证同一个 key 始终映射到同一个假值；Masked 唯一保证单射、还原无歧义。
type KeyMapping struct {
	ID       uint64 `gorm:"primaryKey;autoIncrement"`
	KeyType  string `gorm:"size:32;index:idx_type_orig,unique"` // apikey | phone | ip | session | cookie | jwt
	Original string `gorm:"type:text;index:idx_type_orig,unique"`
	Masked   string `gorm:"type:text;uniqueIndex"`
	Time     uint64 `gorm:"autoCreateTime"`
}
