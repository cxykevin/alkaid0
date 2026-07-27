package lsp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/log"
)

// ClientState LSP 客户端状态
type ClientState int

// LSP 客户端状态
const (
	StateCreated      ClientState = iota // 已创建，未启动
	StateReady                           // 已初始化，可处理请求
	StateShuttingDown                    // 正在关闭
	StateClosed                          // 已关闭
)

func (s ClientState) String() string {
	switch s {
	case StateCreated:
		return "created"
	case StateReady:
		return "ready"
	case StateShuttingDown:
		return "shutting-down"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// Client 管理单个 LSP 服务器进程
type Client struct {
	workdir   string
	language  string
	cmd       *exec.Cmd
	transport *Transport

	state   ClientState
	stateMu sync.Mutex

	lastUsed   time.Time
	lastUsedMu sync.Mutex

	closeOnce sync.Once

	logger *log.LogsObj
}

// NewClient 创建 LSP 客户端
func NewClient(workdir, language string, cfg LanguageServerConfig) *Client {
	return &Client{
		workdir:  workdir,
		language: language,
		state:    StateCreated,
		lastUsed: time.Now(),
		logger:   log.New(fmt.Sprintf("lsp:%s", language)),
	}
}

// Start 启动 LSP 服务器进程并完成 initialize 握手
func (c *Client) Start(ctx context.Context, cfg LanguageServerConfig) error {
	c.stateMu.Lock()
	if c.state != StateCreated {
		c.stateMu.Unlock()
		return fmt.Errorf("client already started (state=%s)", c.state)
	}
	c.state = StateCreated // 保持创建状态，但继续执行
	c.stateMu.Unlock()

	c.logger.Info("starting LSP server: %s %v (workdir=%s)", cfg.Command, cfg.Args, c.workdir)

	// 创建命令
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Dir = c.workdir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	c.cmd = cmd

	// 读取 stderr（后台 goroutine，防止管道阻塞）
	go c.readStderr(stderr)

	// 创建传输层
	c.transport = NewTransport(stdin, stdout)

	// 发送 initialize 请求
	initParams := InitializeParams{
		ProcessID: 0, // 无需进程 ID
		RootURI:   pathToURI(c.workdir),
		Capabilities: ClientCapabilities{
			TextDocument: TextDocumentClientCapabilities{
				Hover: &HoverCapability{
					ContentFormat: []string{"markdown", "plaintext"},
				},
				DocumentSymbol: &DocumentSymbolCapability{
					HierarchicalDocumentSymbolSupport: true,
				},
			},
		},
	}

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := c.transport.SendRequest(initCtx, "initialize", initParams)
	if err != nil {
		c.cleanupProcess()
		return fmt.Errorf("initialize: %w", err)
	}
	_ = result // 暂不处理 capabilities

	// 发送 initialized 通知
	if err := c.transport.SendNotification("initialized", struct{}{}); err != nil {
		c.cleanupProcess()
		return fmt.Errorf("initialized notification: %w", err)
	}

	c.stateMu.Lock()
	c.state = StateReady
	c.stateMu.Unlock()

	c.markUsed()
	c.logger.Info("LSP server ready: %s", cfg.Command)
	return nil
}

// SendRequest 发送请求（线程安全）
func (c *Client) SendRequest(ctx context.Context, method string, params any) ([]byte, error) {
	c.stateMu.Lock()
	if c.state != StateReady {
		c.stateMu.Unlock()
		return nil, fmt.Errorf("client not ready (state=%s)", c.state)
	}
	c.stateMu.Unlock()

	c.markUsed()
	return c.transport.SendRequest(ctx, method, params)
}

// SendNotification 发送通知（线程安全）
func (c *Client) SendNotification(method string, params any) error {
	c.stateMu.Lock()
	if c.state != StateReady {
		c.stateMu.Unlock()
		return fmt.Errorf("client not ready (state=%s)", c.state)
	}
	c.stateMu.Unlock()

	c.markUsed()
	return c.transport.SendNotification(method, params)
}

// Shutdown 优雅关闭 LSP 服务器
func (c *Client) Shutdown(ctx context.Context) error {
	c.stateMu.Lock()
	if c.state != StateReady {
		c.stateMu.Unlock()
		return nil
	}
	c.state = StateShuttingDown
	c.stateMu.Unlock()

	c.logger.Info("shutting down LSP server")

	// 发送 shutdown 请求
	if c.transport != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = c.transport.SendRequest(shutdownCtx, "shutdown", struct{}{})
		cancel()

		_ = c.transport.SendNotification("exit", struct{}{})
	}

	return c.cleanupProcess()
}

// Close 强制关闭 LSP 服务器
func (c *Client) Close() error {
	c.stateMu.Lock()
	c.state = StateClosed
	c.stateMu.Unlock()

	if c.transport != nil {
		_ = c.transport.Close()
	}
	return c.cleanupProcess()
}

// State 返回当前状态
func (c *Client) State() ClientState {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.state
}

// IsIdle 检查客户端是否空闲超时
func (c *Client) IsIdle(timeout time.Duration) bool {
	c.lastUsedMu.Lock()
	defer c.lastUsedMu.Unlock()
	return time.Since(c.lastUsed) > timeout
}

// LastUsed 返回上次使用时间
func (c *Client) LastUsed() time.Time {
	c.lastUsedMu.Lock()
	defer c.lastUsedMu.Unlock()
	return c.lastUsed
}

// Language 返回语言名称
func (c *Client) Language() string {
	return c.language
}

// Workdir 返回工作目录
func (c *Client) Workdir() string {
	return c.workdir
}

// ---------------------------------------------------------------------------
// 内部方法
// ---------------------------------------------------------------------------

// markUsed 更新最后使用时间
func (c *Client) markUsed() {
	c.lastUsedMu.Lock()
	c.lastUsed = time.Now()
	c.lastUsedMu.Unlock()
}

// cleanupProcess 清理子进程
func (c *Client) cleanupProcess() error {
	var err error
	c.closeOnce.Do(func() {
		if c.transport != nil {
			if e := c.transport.Close(); e != nil {
				err = e
			}
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
			// 等待进程退出（短暂超时）
			done := make(chan struct{})
			go func() {
				_ = c.cmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				c.logger.Warn("process did not exit in time")
			}
		}
	})
	c.stateMu.Lock()
	c.state = StateClosed
	c.stateMu.Unlock()
	return err
}

// readStderr 读取 LSP 服务器 stderr 输出（后台 goroutine）
func (c *Client) readStderr(stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		// 只记录非空行
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			c.logger.Debug("[%s stderr] %s", c.language, trimmed)
		}
	}
	if err := scanner.Err(); err != nil && !strings.Contains(err.Error(), "closed") {
		c.logger.Warn("stderr read error: %v", err)
	}
}
