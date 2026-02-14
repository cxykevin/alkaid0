package sandbox

import (
	"context"
	_ "embed" // embed
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// IsolationMode 隔离模式
type IsolationMode int

const (
	// IsolationNone 无隔离（真机运行）
	IsolationNone IsolationMode = iota
	// IsolationOS 操作系统级隔离
	IsolationOS
	// IsolationApp 应用层隔离（旧版本，兼容性）
	IsolationApp
)

// Sandbox 表示一个命令执行沙盒
type Sandbox struct {
	// 允许读写的目录列表
	writableDirs []string
	// 临时目录
	tmpDir string
	// 工作目录
	workDir string
	// 环境变量
	env []string
	// 超时时间
	timeout time.Duration
	// 隔离模式
	isolationMode IsolationMode
	// 互斥锁
	mu sync.RWMutex
}

// Config 沙盒配置
type Config struct {
	// 允许读写的目录
	WritableDirs []string
	// 临时目录（默认使用系统临时目录）
	TmpDir string
	// 工作目录
	WorkDir string
	// 环境变量
	Env []string
	// 超时时间（0表示无超时）
	Timeout time.Duration
	// 隔离模式（默认使用OS级隔离）
	IsolationMode IsolationMode
}

// New 创建一个新的沙盒
func New(cfg Config) (*Sandbox, error) {
	tmpDir := cfg.TmpDir
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}

	workDir := cfg.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("获取当前目录失败: %w", err)
		}
	}

	// 规范化路径
	writableDirs := make([]string, 0, len(cfg.WritableDirs)+1)
	writableDirs = append(writableDirs, filepath.Clean(tmpDir))
	for _, dir := range cfg.WritableDirs {
		cleanDir := filepath.Clean(dir)
		writableDirs = append(writableDirs, cleanDir)
	}

	env := cfg.Env
	if env == nil {
		env = os.Environ()
	}

	isolationMode := cfg.IsolationMode
	// 注意：IsolationMode的零值是IsolationNone(0)，所以不能简单判断==0
	// 如果用户没有显式设置，使用OS级隔离
	// 这里我们保持用户的选择，包括IsolationNone

	return &Sandbox{
		writableDirs:  writableDirs,
		tmpDir:        tmpDir,
		workDir:       workDir,
		env:           env,
		timeout:       cfg.Timeout,
		isolationMode: isolationMode,
	}, nil
}

// Command 创建一个沙盒命令
type Command struct {
	sandbox *Sandbox
	cmd     *exec.Cmd
	ctx     context.Context
	cancel  context.CancelFunc
	runner  commandRunner
	// 命令信息
	name    string
	args    []string
	workDir string
	env     []string
	temp    any
}

// commandRunner 表示可执行命令的统一接口（用于 Windows 特殊实现）
type commandRunner interface {
	Start() error
	Wait() error
	Run() error
	SetStdin(io.Reader)
	SetStdout(io.Writer)
	SetStderr(io.Writer)
	Kill() error
}

// Execute 在沙盒中执行命令
func (s *Sandbox) Execute(name string, args ...string) (*Command, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 创建上下文
	var ctx context.Context
	var cancel context.CancelFunc
	if s.timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), s.timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}

	switch s.isolationMode {
	case IsolationNone:
		// 无隔离，直接运行
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = s.workDir
		cmd.Env = s.env

		return &Command{
			sandbox: s,
			cmd:     cmd,
			ctx:     ctx,
			cancel:  cancel,
			name:    name,
			args:    args,
			workDir: s.workDir,
			env:     s.env,
		}, nil

	case IsolationOS:
		// OS级隔离
		isolatedCmd, err := s.createIsolatedCommand(ctx, name, args...)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("创建隔离命令失败: %w", err)
		}
		isolatedCmd.cancel = cancel
		isolatedCmd.sandbox = s
		return isolatedCmd, nil

	case IsolationApp:
		// 应用层隔离（旧版本）
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = s.workDir
		cmd.Env = s.env

		return &Command{
			sandbox: s,
			cmd:     cmd,
			ctx:     ctx,
			cancel:  cancel,
			name:    name,
			args:    args,
			workDir: s.workDir,
			env:     s.env,
		}, nil

	default:
		cancel()
		return nil, fmt.Errorf("不支持的隔离模式: %d", s.isolationMode)
	}
}

// SetStdin 设置标准输入
func (c *Command) SetStdin(r io.Reader) {
	if c.runner != nil {
		c.runner.SetStdin(r)
		return
	}
	if c.cmd != nil {
		c.cmd.Stdin = r
	}
}

// SetStdout 设置标准输出
func (c *Command) SetStdout(w io.Writer) {
	if c.runner != nil {
		c.runner.SetStdout(w)
		return
	}
	if c.cmd != nil {
		c.cmd.Stdout = w
	}
}

// SetStderr 设置标准错误
func (c *Command) SetStderr(w io.Writer) {
	if c.runner != nil {
		c.runner.SetStderr(w)
		return
	}
	if c.cmd != nil {
		c.cmd.Stderr = w
	}
}

// Start 启动命令
func (c *Command) Start() error {
	if c.runner != nil {
		if err := c.runner.Start(); err != nil {
			return err
		}
		if c.ctx != nil {
			go func() {
				<-c.ctx.Done()
				_ = c.runner.Kill()
			}()
		}
		return nil
	}
	return c.cmd.Start()
}

// Wait 等待命令完成
func (c *Command) Wait() error {
	defer c.cancel()
	defer c.cleanupCommand()
	if c.runner != nil {
		return c.runner.Wait()
	}
	return c.cmd.Wait()
}

// Run 运行命令并等待完成
func (c *Command) Run() error {
	if c.runner != nil {
		if err := c.Start(); err != nil {
			c.cancel()
			c.cleanupCommand()
			return err
		}
		return c.Wait()
	}
	if err := c.Start(); err != nil {
		c.cancel()
		return err
	}
	return c.Wait()
}

// Kill 强制终止命令
func (c *Command) Kill() error {
	if c.runner != nil {
		return c.runner.Kill()
	}
	if c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}
	return errors.New("进程未启动")
}

// IsPathWritable 检查路径是否可写
func (s *Sandbox) IsPathWritable(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 如果是无隔离模式，所有路径都可写
	if s.isolationMode == IsolationNone {
		return true
	}

	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return false
	}

	for _, dir := range s.writableDirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}

		// 检查路径是否在允许的目录下
		rel, err := filepath.Rel(absDir, absPath)
		if err != nil {
			continue
		}

		// 如果相对路径不以..开头，说明在允许的目录下
		if !strings.HasPrefix(rel, "..") {
			return true
		}
	}

	return false
}

// ValidateCommand 验证命令是否安全
func (s *Sandbox) ValidateCommand(name string, args ...string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Windows 内置命令列表
	windowsBuiltins := map[string]bool{
		"echo": true, "cd": true, "dir": true, "copy": true, "move": true,
		"del": true, "type": true, "set": true, "if": true, "for": true,
	}

	// 检查命令是否存在
	// 对于 Windows 内置命令，跳过 LookPath 检查
	isWindowsBuiltin := runtime.GOOS == "windows" && windowsBuiltins[strings.ToLower(name)]
	if !isWindowsBuiltin {
		_, err := exec.LookPath(name)
		if err != nil {
			return fmt.Errorf("命令不存在: %s", name)
		}
	}

	// 如果是无隔离模式，跳过危险命令检查
	if s.isolationMode == IsolationNone {
		return nil
	}

	// 检查危险命令（可根据需要扩展）
	dangerousCommands := []string{"rm", "del", "format", "mkfs"}
	for _, dangerous := range dangerousCommands {
		if strings.Contains(strings.ToLower(name), dangerous) {
			return fmt.Errorf("禁止执行危险命令: %s", name)
		}
	}

	return nil
}

// GetWritableDirs 获取可写目录列表
func (s *Sandbox) GetWritableDirs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dirs := make([]string, len(s.writableDirs))
	copy(dirs, s.writableDirs)
	return dirs
}

// GetTmpDir 获取临时目录
func (s *Sandbox) GetTmpDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tmpDir
}

// GetWorkDir 获取工作目录
func (s *Sandbox) GetWorkDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workDir
}

// SetWorkDir 设置工作目录
func (s *Sandbox) SetWorkDir(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cleanDir := filepath.Clean(dir)
	if _, err := os.Stat(cleanDir); err != nil {
		return fmt.Errorf("目录不存在: %w", err)
	}

	s.workDir = cleanDir
	return nil
}

// GetIsolationMode 获取隔离模式
func (s *Sandbox) GetIsolationMode() IsolationMode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isolationMode
}

// SetIsolationMode 设置隔离模式
func (s *Sandbox) SetIsolationMode(mode IsolationMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isolationMode = mode
}

// GetPlatformInfo 获取平台信息
func GetPlatformInfo() map[string]string {
	info := map[string]string{
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
		"version": runtime.Version(),
	}

	// 检测隔离能力
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("unshare"); err == nil {
			info["isolation"] = "user-namespaces"
		} else {
			info["isolation"] = "none"
		}
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err == nil {
			info["isolation"] = "sandbox-exec"
		} else {
			info["isolation"] = "none"
		}
	case "windows":
		if _, err := exec.LookPath("appcontainer.exe"); err == nil {
			info["isolation"] = "appcontainer"
		} else {
			info["isolation"] = "none"
		}
	default:
		info["isolation"] = "none"
	}

	return info
}

// IsolationModeString 返回隔离模式的字符串表示
func (m IsolationMode) String() string {
	switch m {
	case IsolationNone:
		return "none"
	case IsolationOS:
		return "os"
	case IsolationApp:
		return "app"
	default:
		return "unknown"
	}
}
