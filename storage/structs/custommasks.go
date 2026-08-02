package structs

// CustomMask 用户通过 /mask add 手动新增的自定义脱敏值。
// 这些值在请求出站前会被替换为同格式假值（与 KeyMapping 映射配合），
// 并在流式响应中还原为原文；/mask del 删除后不再脱敏。
type CustomMask struct {
	ID    uint64 `gorm:"primaryKey;autoIncrement"`
	Value string `gorm:"type:text;uniqueIndex"`
	Time  uint64 `gorm:"autoCreateTime"`
}
