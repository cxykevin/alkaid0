package mask

import (
	"fmt"
	"strings"

	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// AddCustom 将 value 加入自定义脱敏值列表（幂等：已存在则忽略）。
// 后续请求出站前会把 value 替换为同格式假值，并在响应中还原。
func AddCustom(db *gorm.DB, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("mask value is empty")
	}
	return db.Where("value = ?", value).FirstOrCreate(&storageStructs.CustomMask{Value: value}).Error
}

// DelCustom 移除 value 并清理已生成的脱敏映射（原值→假值），
// 避免删除后旧假值在响应中仍被还原。
func DelCustom(db *gorm.DB, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("mask value is empty")
	}
	if err := db.Where("value = ?", value).Delete(&storageStructs.CustomMask{}).Error; err != nil {
		return err
	}
	return db.Where("key_type = ? AND original = ?", TypeCustom, value).Delete(&storageStructs.KeyMapping{}).Error
}
