package storage

import (
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// GlobalConfig 全局配置缓存，启动时从数据库加载
var GlobalConfig = structs.Configs{}

// ReadGlobalConfigs 从数据库读取全局配置并存入 GlobalConfig
func ReadGlobalConfigs(db *gorm.DB) error {
	var list []structs.Configs
	if err := db.Limit(1).Find(&list).Error; err != nil {
		return err
	}
	if len(list) > 0 {
		GlobalConfig = list[0]
	}
	return nil
}

// SaveGlobalConfigs 保存全局配置
// Configs 无主键，GORM Save/First 会报 "WHERE conditions required"，故用清空后重插保持单行语义
func SaveGlobalConfigs(db *gorm.DB) error {
	if err := db.Where("1 = 1").Delete(&structs.Configs{}).Error; err != nil {
		return err
	}
	return db.Create(&GlobalConfig).Error
}
