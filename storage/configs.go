package storage

import (
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// GlobalConfig 全局配置
var GlobalConfig = structs.Configs{}

// ReadGlobalConfigs 读取全局配置
func ReadGlobalConfigs(db *gorm.DB) error {
	return db.Order("rowid").First(&GlobalConfig).Error
}

// SaveGlobalConfigs 保存全局配置
func SaveGlobalConfigs(db *gorm.DB) error {
	return db.Save(&GlobalConfig).Error
}
