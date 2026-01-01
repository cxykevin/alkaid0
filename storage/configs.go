package storage

import "github.com/cxykevin/alkaid0/storage/structs"

// GlobalConfig 全局配置
var GlobalConfig = structs.Configs{}

// ReadGlobalConfigs 读取全局配置
func ReadGlobalConfigs() error {
	return DB.Order("rowid").First(&GlobalConfig).Error
}

// SaveGlobalConfigs 保存全局配置
func SaveGlobalConfigs() error {
	return DB.Save(&GlobalConfig).Error
}
