package structs

// Configs 全局表
// 注意：不要给该表加主键列——SQLite 无法对已有表执行 "ADD PRIMARY KEY"，
// 会破坏 AutoMigrate 并导致旧数据库升级失败。读写请走 storage/configs.go 的
// Find/Delete+Create 方案（GORM Save/First 对无主键模型会报 "WHERE conditions required"）。
type Configs struct {
	LastChatID uint32
}
