package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/product"
)

// GlobalConfig 配置文件对象
var GlobalConfig = &structs.Config{}

const defaultConfigPath = "~/.config/alkaid0/config.json"
const envConfigName = "ALKAID0_CONFIG_PATH"

var configPath string

// Path 返回当前配置文件路径。
// 优先级：ALKAID0_CONFIG_PATH 环境变量 > 默认路径 (~/.config/alkaid0/config.json)
func Path() string {
	if configPath == "" {
		if path := os.Getenv(envConfigName); path != "" {
			configPath = path
		} else {
			configPath = defaultConfigPath
		}
	}
	return configPath
}

// Load 加载配置文件。
// 先初始化默认配置（含产品版本号和默认模型），然后尝试从文件系统读取 JSON 配置。
// 文件不存在或解析失败时会备份原文件（加上 .bak 后缀）并用默认配置兜底。
func Load() {
	// 第 1 步：使用默认配置初始化（作为任何解析失败的 fallback）
	model := structs.ModelsConfig{}
	model = structs.BuildDefault(model)
	GlobalConfig = &structs.Config{
		Version: product.VersionID,
		Model:   model,
	}

	// 第 2 步：确定配置文件路径
	if path := os.Getenv(envConfigName); path != "" {
		configPath = path
	} else {
		configPath = defaultConfigPath
	}

	// 第 3 步：展开用户目录并确保目录存在
	expandedPath := configutil.ExpandPath(configPath)
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	// 第 4 步：读取并解析配置文件
	data, err := os.ReadFile(expandedPath)
	if err != nil {
		// 文件不存在或读取失败时备份旧文件并创建新配置
		if _, backupErr := os.Stat(expandedPath); backupErr == nil {
			backupPath := expandedPath + ".bak"
			_ = os.Rename(expandedPath, backupPath)
		}
		Save()
		return
	}

	// 第 5 步：JSON 反序列化
	if err := json.Unmarshal(data, GlobalConfig); err != nil {
		// 解析失败时备份原文件
		backupPath := expandedPath + ".bak"
		_ = os.Rename(expandedPath, backupPath)
		return
	}
}

// Save 将当前配置序列化为 JSON 并写入配置文件。
// 写入完成后触发所有注册的重载钩子（reloadHooks），用于通知其他模块配置已变更。
func Save() {
	if configPath == "" {
		Load()
	}

	expandedPath := configutil.ExpandPath(configPath)
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	data, err := json.MarshalIndent(GlobalConfig, "", "  ")
	if err != nil {
		return
	}

	os.WriteFile(expandedPath, data, 0644)
	for _, hook := range reloadHooks {
		hook()
	}
}

// reloadHooks 配置重载时的回调函数列表
var reloadHooks []func()

// AddReloadHook 注册配置重载后的回调钩子
func AddReloadHook(hook func()) {
	reloadHooks = append(reloadHooks, hook)
}

// Reload 重新加载配置文件并触发所有注册的重载回调。
// 用于运行时配置热更新，如修改模型参数后无需重启进程。
func Reload() {
	Load()
	for _, hook := range reloadHooks {
		hook()
	}
}
