package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/product"
)

// GlobalConfig 配置文件对象。注意：Load/Save/Reload 会写锁保护，直接读字段是线程安全的
//（写操作发生在启动时和管理 RPC 中），但严格并发安全应使用 GlobalConfig() 函数。
var GlobalConfig = &structs.Config{}

const defaultConfigPath = "~/.config/alkaid0/config.json"
const envConfigName = "ALKAID0_CONFIG_PATH"

var (
	globalConfigMu sync.RWMutex
	configPath     string
)

// GlobalConfig 返回当前配置的读安全快照
// 多次调用可能返回不同版本，但保证指针有效且无数据竞争
func GlobalConfigSafe() *structs.Config {
	globalConfigMu.RLock()
	defer globalConfigMu.RUnlock()
	return GlobalConfig
}

// GlobalConfigForWrite 返回写锁下的配置指针，调用者必须调用解锁函数
func GlobalConfigForWrite() (*structs.Config, func()) {
	globalConfigMu.Lock()
	return GlobalConfig, func() { globalConfigMu.Unlock() }
}

// GlobalConfigSwap 原子替换配置并返回恢复函数。适合测试使用
func GlobalConfigSwap(cfg structs.Config) func() {
	globalConfigMu.Lock()
	old := *GlobalConfig
	*GlobalConfig = cfg
	globalConfigMu.Unlock()
	return func() {
		globalConfigMu.Lock()
		*GlobalConfig = old
		globalConfigMu.Unlock()
	}
}

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

// generateKey 生成一个以 "alk-" 开头、长度 > 12 的随机密钥
func generateKey() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return "alk-" + hex.EncodeToString(b)
}

// ensureKey 检查 Server.Key 是否为空，若为空则自动生成并保存配置
func ensureKey() {
	if GlobalConfig.Server.Key == "" {
		GlobalConfig.Server.Key = generateKey()
		Save()
	}
}

// Load 加载配置文件。
// 先初始化默认配置（含产品版本号和默认模型），然后尝试从文件系统读取 JSON 配置。
// 文件不存在或解析失败时会备份原文件（加上 .bak 后缀）并用默认配置兜底。
// 加载完成后若 Server.Key 为空则自动生成随机密钥并保存。
func Load() {
	// 使用默认配置初始化（作为任何解析失败的 fallback）
	model := structs.ModelsConfig{}
	model = structs.BuildDefault(model)

	globalConfigMu.Lock()
	GlobalConfig = &structs.Config{
		Version: product.VersionID,
		Model:   model,
	}
	globalConfigMu.Unlock()

	// 确定配置文件路径
	if path := os.Getenv(envConfigName); path != "" {
		configPath = path
	} else {
		configPath = defaultConfigPath
	}

	// 展开用户目录并确保目录存在
	expandedPath := configutil.ExpandPath(configPath)
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	// 读取并解析配置文件
	data, err := os.ReadFile(expandedPath)
	if err != nil {
		// 文件不存在或读取失败时备份旧文件并创建新配置
		if _, backupErr := os.Stat(expandedPath); backupErr == nil {
			backupPath := expandedPath + ".bak"
			_ = os.Rename(expandedPath, backupPath)
		}
		Save()
		// 新创建的配置文件需要自动生成密钥
		ensureKey()
		return
	}

	// JSON 反序列化
	globalConfigMu.Lock()
	if err := json.Unmarshal(data, GlobalConfig); err != nil {
		globalConfigMu.Unlock()
		// 解析失败时备份原文件
		backupPath := expandedPath + ".bak"
		_ = os.Rename(expandedPath, backupPath)
		return
	}
	globalConfigMu.Unlock()

	// 加载完成后检查密钥，为空则自动生成
	ensureKey()
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

	globalConfigMu.RLock()
	data, err := json.MarshalIndent(GlobalConfig, "", "  ")
	globalConfigMu.RUnlock()
	if err != nil {
		return
	}

	os.WriteFile(expandedPath, data, 0644)
	fireReloadHooks()
}

// reloadHooks 配置重载时的回调函数列表
var (
	reloadHooksMu sync.RWMutex
	reloadHooks   []func()
)

// AddReloadHook 注册配置重载后的回调钩子
func AddReloadHook(hook func()) {
	reloadHooksMu.Lock()
	reloadHooks = append(reloadHooks, hook)
	reloadHooksMu.Unlock()
}

// fireReloadHooks 触发所有注册的重载回调
func fireReloadHooks() {
	reloadHooksMu.RLock()
	hooks := reloadHooks
	reloadHooksMu.RUnlock()
	for _, hook := range hooks {
		hook()
	}
}

// Reload 重新加载配置文件并触发所有注册的重载回调。
// 用于运行时配置热更新，如修改模型参数后无需重启进程。
func Reload() {
	Load()
	fireReloadHooks()
}
