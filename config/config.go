package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/product"
)

// GlobalConfig 配置文件对象
var GlobalConfig = &structs.Config{}

const defaultConfigPath = "~/.config/alkaid0/config.json"
const envConfigName = "ALKAID0_CONFIG_PATH"

var configPath string

// Load 加载配置文件
func Load() {
	// 默认配置
	model := structs.ModelsConfig{}
	model = structs.BuildDefault(model)
	GlobalConfig = &structs.Config{
		Version: product.VersionID,
		Model:   model,
	}

	// 读取环境变量
	if path := os.Getenv(envConfigName); path != "" {
		configPath = path
	} else {
		configPath = defaultConfigPath
	}

	// 展开用户目录路径
	expandedPath := os.ExpandEnv(configPath)

	// 确保目录存在
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		// 目录创建失败，使用默认配置
		return
	}

	// 读取配置文件
	data, err := os.ReadFile(expandedPath)
	if err != nil {
		// 文件不存在或读取失败，备份旧文件并创建新配置
		if os.IsNotExist(err) {
			// 创建默认配置
			Save()
			return
		}

		// 如果是其他错误，尝试备份旧文件
		if _, backupErr := os.Stat(expandedPath); backupErr == nil {
			backupPath := expandedPath + ".bak"
			os.Rename(expandedPath, backupPath)
		}

		// 创建默认配置
		Save()
		return
	}

	// 解析配置文件
	if err := json.Unmarshal(data, GlobalConfig); err != nil {
		Save()
		return
	}
}

// Save 保存配置文件
func Save() {
	// 确保配置路径已设置
	if configPath == "" {
		Load()
	}

	// 展开用户目录路径
	expandedPath := os.ExpandEnv(configPath)

	// 确保目录存在
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	// 序列化配置
	data, err := json.MarshalIndent(GlobalConfig, "", "  ")
	if err != nil {
		return
	}

	// 写入配置文件
	os.WriteFile(expandedPath, data, 0644)
}
