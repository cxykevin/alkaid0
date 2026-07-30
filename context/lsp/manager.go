package lsp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
)

// ErrLSPDisabled 当 LSP 连续启动失败被禁用时返回
var ErrLSPDisabled = errors.New("LSP disabled after consecutive failures")

// Manager 管理多工作目录多语言的 LSP 客户端
type Manager struct {
	clients   map[string]*Client // key = languageKey(workdir, language)
	clientsMu sync.Mutex

	idleTimeout time.Duration

	// failCount 记录每个 LSP 的连续启动失败次数 (key = languageKey)
	// 达到阈值后本次索引周期内禁用该 LSP，避免反复尝试启动
	failCount   map[string]int
	failCountMu sync.Mutex

	stopReaper chan struct{}
	reaperWG   sync.WaitGroup
}

// globalManager 全局管理器实例
var globalManager *Manager

// Initialize 初始化 LSP 管理器
// 从配置读取设置，若未启用则直接返回
func Initialize() error {
	cfg := config.GlobalConfigSafe()
	if !cfg.Context.LSP.Enabled {
		logger.Info("LSP client disabled by config")
		return nil
	}

	timeout := time.Duration(cfg.Context.LSP.IdleTimeout) * time.Second
	if timeout <= 0 {
		timeout = 600 * time.Second // 默认 10 分钟
	}

	globalManager = &Manager{
		clients:     make(map[string]*Client),
		failCount:   make(map[string]int),
		idleTimeout: timeout,
		stopReaper:  make(chan struct{}),
	}

	// 启动回收 goroutine
	globalManager.reaperWG.Add(1)
	go globalManager.reaperLoop()

	logger.Info("LSP manager initialized (idleTimeout=%v)", timeout)

	// 注册配置热重载钩子
	config.AddReloadHook(func() {
		cfg := config.GlobalConfigSafe()
		newTimeout := time.Duration(cfg.Context.LSP.IdleTimeout) * time.Second
		if newTimeout > 0 {
			globalManager.clientsMu.Lock()
			globalManager.idleTimeout = newTimeout
			globalManager.clientsMu.Unlock()
		}
	})

	return nil
}

// Shutdown 关闭所有 LSP 客户端
func Shutdown() error {
	if globalManager == nil {
		return nil
	}

	logger.Info("shutting down all LSP clients")

	// 停止回收 goroutine
	close(globalManager.stopReaper)
	globalManager.reaperWG.Wait()

	// 关闭所有客户端
	globalManager.clientsMu.Lock()
	clients := make([]*Client, 0, len(globalManager.clients))
	for _, c := range globalManager.clients {
		clients = append(clients, c)
	}
	globalManager.clients = make(map[string]*Client)
	globalManager.clientsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var lastErr error
	for _, c := range clients {
		if err := c.Shutdown(ctx); err != nil {
			logger.Warn("shutdown client %s|%s: %v", c.Workdir(), c.Language(), err)
			lastErr = err
		}
	}

	return lastErr
}

// GetSymbols 获取文件符号（对外 API）
// workdir: 工作目录
// filePath: 文件绝对路径
func GetSymbols(workdir, filePath string) ([]SymbolResult, error) {
	if globalManager == nil {
		return nil, fmt.Errorf("LSP manager not initialized (call Initialize() first)")
	}
	return globalManager.GetSymbols(workdir, filePath)
}

// ResetLSPFailures 重置所有 LSP 的失败计数，供新索引周期开始前调用
func ResetLSPFailures() {
	if globalManager == nil {
		return
	}
	globalManager.failCountMu.Lock()
	for k := range globalManager.failCount {
		delete(globalManager.failCount, k)
	}
	globalManager.failCountMu.Unlock()
	logger.Info("reset LSP failure counters")
}

// ---------------------------------------------------------------------------
// 内部方法
// ---------------------------------------------------------------------------

// getClient 获取或创建 LSP 客户端
// 连续启动失败 3 次后，该工作目录下对应语言的 LSP 会被禁用（避免反复超时）
func (m *Manager) getClient(workdir, filePath string) (*Client, error) {
	ext := extFromPath(filePath)
	langID := languageIDFromExt(ext)

	// 解析语言服务器配置
	serverCfg, err := resolveLanguageServer(ext)
	if err != nil {
		return nil, fmt.Errorf("unsupported file type %s: %w", ext, err)
	}

	key := languageKey(workdir, langID)

	// 检查是否已被连续失败禁用
	m.failCountMu.Lock()
	if m.failCount[key] >= 3 {
		m.failCountMu.Unlock()
		return nil, fmt.Errorf("%w (key=%s)", ErrLSPDisabled, key)
	}
	m.failCountMu.Unlock()

	// 查找已存在的客户端
	m.clientsMu.Lock()
	if client, ok := m.clients[key]; ok {
		if client.State() == StateReady {
			m.clientsMu.Unlock()
			return client, nil
		}
		// 客户端已关闭或不正常，删除并重建
		logger.Warn("client %s in state %s, recreating", key, client.State())
		delete(m.clients, key)
	}
	m.clientsMu.Unlock()

	// 创建新客户端
	client := NewClient(workdir, langID, serverCfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Start(ctx, serverCfg); err != nil {
		m.failCountMu.Lock()
		m.failCount[key]++
		count := m.failCount[key]
		m.failCountMu.Unlock()
		logger.Warn("LSP %s start failed (%d/3): %v", key, count, err)
		return nil, fmt.Errorf("start LSP %s: %w", key, err)
	}

	// 启动成功，重置失败计数
	m.failCountMu.Lock()
	delete(m.failCount, key)
	m.failCountMu.Unlock()

	m.clientsMu.Lock()
	m.clients[key] = client
	m.clientsMu.Unlock()

	logger.Info("created LSP client: %s (cmd=%s)", key, serverCfg.Command)
	return client, nil
}

// reaperLoop 定期回收空闲客户端
func (m *Manager) reaperLoop() {
	defer m.reaperWG.Done()

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopReaper:
			return
		case <-ticker.C:
			m.reapIdle()
		}
	}
}

// reapIdle 回收所有空闲超时的客户端
func (m *Manager) reapIdle() {
	m.clientsMu.Lock()
	clients := make(map[string]*Client, len(m.clients))
	maps.Copy(clients, m.clients)
	m.clientsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var idleKeys []string
	for key, c := range clients {
		c.lastUsedMu.Lock()
		idle := time.Since(c.lastUsed) > m.idleTimeout
		c.lastUsedMu.Unlock()

		if idle {
			idleKeys = append(idleKeys, key)
		}
	}

	if len(idleKeys) == 0 {
		return
	}

	m.clientsMu.Lock()
	for _, key := range idleKeys {
		if c, ok := m.clients[key]; ok {
			delete(m.clients, key)
			go func(k string, cl *Client) {
				if err := cl.Shutdown(ctx); err != nil {
					logger.Warn("reap idle client %s: %v", k, err)
				} else {
					logger.Info("recycled idle LSP client: %s", k)
				}
			}(key, c)
		}
	}
	m.clientsMu.Unlock()
}
