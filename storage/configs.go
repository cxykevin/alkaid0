package storage

import (
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// GlobalConfig 全局配置缓存，启动时从数据库加载
var GlobalConfig = structs.Configs{}

// ReadGlobalConfigs 从数据库读取全局配置并存入 GlobalConfig
func ReadGlobalConfigs(db *gorm.DB) error {
	return db.Order("rowid").First(&GlobalConfig).Error
}

// SaveGlobalConfigs 保存全局配置
func SaveGlobalConfigs(db *gorm.DB) error {
	return db.Save(&GlobalConfig).Error
}
