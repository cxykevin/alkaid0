package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/product"
)

// GlobalConfig 当前生效的配置对象。
//
// 不变量（写时复制 / copy-on-write）：对象一旦发布，*structs.Config 及其内部的
// map/slice 就**不再被修改**。所有写入方必须先克隆副本（GlobalConfigForWrite）
// 或构造全新对象（Load / GlobalConfigSwap），然后整体替换指针。
//
// 正因为如此，全仓库 200+ 处 config.GlobalConfig 直接读取才不需要加锁。
// 任何"就地修改已发布对象"的写法（例如 *GlobalConfig = x 或
// GlobalConfig.Model.Models[k] = v）都会与正在遍历这些 map 的读者并发，
// 触发 Go 运行时的 fatal("concurrent map read and map write") 并终止整个进程。
var GlobalConfig = &structs.Config{}

const defaultConfigPath = "~/.config/alkaid0/config.json"
const envConfigName = "ALKAID0_CONFIG_PATH"

var (
	globalConfigMu sync.RWMutex
	configPath     string
)

// GlobalConfigSafe 返回当前配置快照。
// 由于配置采用写时复制，返回的指针在其生命周期内不会再被修改，可安全读取；
// 但它不保证是"最新版本"——多次调用可能拿到不同的配置对象。
func GlobalConfigSafe() *structs.Config {
	globalConfigMu.RLock()
	defer globalConfigMu.RUnlock()
	return GlobalConfig
}

// SnapshotJSON 在读锁内把当前配置序列化为 JSON 快照。
// 供 RPC 层返回配置：调用方拿到的是与后续写入完全解耦的字节流，
// 不会在序列化过程中被并发写改到（json.Marshal 会遍历各 map）。
func SnapshotJSON() (json.RawMessage, error) {
	globalConfigMu.RLock()
	defer globalConfigMu.RUnlock()
	b, err := json.Marshal(GlobalConfig)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// cloneConfig 深拷贝配置（JSON 往返）。配置结构体全部可由 JSON 无损往返：
// 无 json:"-" 字段、无自定义 Marshal、无接口类型字段。
func cloneConfig(cfg *structs.Config) (*structs.Config, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	out := &structs.Config{}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, err
	}
	return out, nil
}

// GlobalConfigForWrite 以写时复制方式修改配置，返回当前配置的深拷贝。
//
// 调用方修改副本后必须调用 commit() 发布，或调用 discard() 放弃；
// 二者都只能在修改完成后立即调用一次。函数内部持有写锁直到 commit/discard，
// 因此"读-改-写"整体互斥，不会丢失并发更新（与旧的解锁函数语义一致），
// 区别是改动只会在 commit 时以整体替换指针的方式发布。
func GlobalConfigForWrite() (cfg *structs.Config, commit func(), discard func()) {
	globalConfigMu.Lock()
	clone, err := cloneConfig(GlobalConfig)
	if err != nil {
		// 理论上不可达（配置结构体全部可 JSON 往返）。退化为旧的就地修改语义，
		// 仍然保证锁一定被释放，调用方流程不倒挂。
		clone = GlobalConfig
	}
	done := false
	commit = func() {
		if done {
			return
		}
		done = true
		GlobalConfig = clone
		globalConfigMu.Unlock()
	}
	discard = func() {
		if done {
			return
		}
		done = true
		globalConfigMu.Unlock()
	}
	return clone, commit, discard
}

// GlobalConfigSwap 原子替换配置并返回恢复函数。适合测试使用。
// 写时复制：直接替换指针，绝不就地改写已发布的对象。
func GlobalConfigSwap(cfg structs.Config) func() {
	globalConfigMu.Lock()
	old := GlobalConfig
	GlobalConfig = &cfg
	globalConfigMu.Unlock()
	return func() {
		globalConfigMu.Lock()
		GlobalConfig = old
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
// 在写锁下修改 Server.Key，避免与运行时无锁读的字段（如各 RPC handler）形成数据竞争
func ensureKey() {
	cfg, commit, discard := GlobalConfigForWrite()
	if cfg.Server.Key == "" {
		cfg.Server.Key = generateKey()
		commit()
		Save()
		return
	}
	discard()
}

// Load 加载配置文件。
// 先初始化默认配置（含产品版本号和默认模型），然后尝试从文件系统读取 JSON 配置。
// 文件不存在或解析失败时会备份原文件（加上 .bak 后缀）并用默认配置兜底。
// 加载完成后若 Server.Key 为空则自动生成随机密钥并保存。
func Load() {
	// 使用默认配置初始化（作为任何解析失败的 fallback）。
	// 用 BuildDefault 构建完整配置，确保 Server.Host/Port/Path 等字段有默认值
	// （否则全新安装时 WebSocket 服务器会因空 Host/Port/Path 而启动失败）。
	model := structs.BuildDefault(structs.ModelsConfig{})
	cfg := structs.BuildDefault(structs.Config{})
	cfg.Version = product.VersionID
	cfg.Model = model
	EnsureOnlineSearchDefaults(&cfg, nil)

	globalConfigMu.Lock()
	GlobalConfig = &cfg
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
		if !errors.Is(err, fs.ErrNotExist) {
			// 文件存在但读取失败（权限、I/O 错误、路径其实是目录等）。
			// 此前一律走"改名备份 + Save() 默认配置"分支，等于把用户的配置文件
			// 覆盖成默认值（配置被清空），而读取失败往往只是暂时性的。
			// 现在保留磁盘上的原文件不动，仅以默认配置在内存中继续运行并大声报错。
			// 注意：不调用 ensureKey()，以避免其内部 Save() 再次覆盖原文件。
			fmt.Fprintf(os.Stderr, "config: read %s failed: %v (kept the existing file untouched, running with in-memory defaults)\n", expandedPath, err)
			return
		}
		// 文件确实不存在（首次运行）：落盘默认配置并生成密钥
		Save()
		// 新创建的配置文件需要自动生成密钥
		ensureKey()
		return
	}

	// JSON 反序列化到临时变量，避免解析失败时污染 GlobalConfig
	tempConfig := &structs.Config{}
	if err := json.Unmarshal(data, tempConfig); err != nil {
		// 解析失败时备份原文件，GlobalConfig 保持前面设置的默认值
		backupPath := expandedPath + ".bak"
		_ = os.Rename(expandedPath, backupPath)
		// 不调用 Save()，GlobalConfig 已是干净默认值
		// ensureKey() 会在需要时自动触发 Save()
		ensureKey()
		return
	}

	// 解析成功：先把默认值补齐到**尚未发布**的 tempConfig 上，再整体替换指针发布。
	// 注意不能写成 *GlobalConfig = *tempConfig —— 那会就地改写已经发布出去的对象，
	// 与正在遍历其 map 的读者并发时 Go 运行时会 fatal 终止进程（写时复制不变量）。
	EnsureOnlineSearchDefaults(tempConfig, json.RawMessage(data))
	EnsureCacheDefaults(tempConfig, json.RawMessage(data))
	globalConfigMu.Lock()
	GlobalConfig = tempConfig
	globalConfigMu.Unlock()

	// 加载完成后检查密钥，为空则自动生成
	ensureKey()
}

// Save 将当前配置序列化为 JSON 并写入配置文件。
// 采用原子写入（临时文件 + 重命名），避免写入失败/中断时破坏原配置；
// 仅在写入成功后才触发重载钩子。错误返回给调用方，不再静默吞掉。
func Save() error {
	if configPath == "" {
		Load()
	}

	expandedPath := configutil.ExpandPath(configPath)
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	globalConfigMu.RLock()
	data, err := json.MarshalIndent(GlobalConfig, "", "  ")
	globalConfigMu.RUnlock()
	if err != nil {
		return err
	}

	// 原子写入：先写临时文件再重命名
	tmp := expandedPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, expandedPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	fireReloadHooks()
	return nil
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
