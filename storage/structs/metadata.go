package structs

import "time"

// Metadata 数据库元信息表（通用键值对，表名固定为 metadata）。
//
// 用途：记录 schema 版本等数据库级元信息。树形消息迁移（见 storage/migrate）
// 以「metadata 表是否存在」作为一次性迁移是否已执行的标记：迁移函数创建该表并
// 写入 schema_version / migrated_at；此后即使表中没有版本记录也视为已迁移，
// 不再重复执行。后续 schema 变更应通过表中的版本号驱动。
//
// 注意：本表**故意不加入 Tables**（InitDB 的 AutoMigrate 表清单）。若加入，
// InitDB 会在迁移函数检查「metadata 表是否存在」之前就把表建出来，历史库的
// 线性消息将永远不会被回填。表由迁移函数在事务内自行创建。
type Metadata struct {
	Key       string `gorm:"primaryKey"`
	Value     string
	UpdatedAt time.Time
}

// TableName 固定表名为 metadata（小写单数），避免 GORM 复数化生成 metadatas。
func (Metadata) TableName() string {
	return "metadata"
}
