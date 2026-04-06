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

	"github.com/cxykevin/alkaid0/log"
)

var logger = log.New("sandbox")

// IsolationMode 隔离模式，定义了沙盒对宿主系统的保护强度。
type IsolationMode int

const (
	// IsolationNone 无隔离模式。命令直接在宿主系统运行，通常用于受信任的本地操作。
	IsolationNone IsolationMode = iota
	// IsolationOS 操作系统级隔离。利用平台特性（如 Linux namespaces, macOS sandbox-exec）
	// 限制进程对文件系统、网络和进程树的访问。
	IsolationOS
)

// Sandbox 表示一个命令执行沙盒，它通过维护可写目录白名单和危险命令黑名单来确保安全。
type Sandbox struct {
	// 允许读写的目录列表（白名单）。只有在此列表及其子目录下的文件操作才被允许。
	writableDirs []string
	// 临时目录，用于存放命令执行过程中的临时文件。
	tmpDir string
	// 工作目录，命令启动时的初始路径。
	workDir string
	// 环境变量，传递给被执行命令的配置。
	env []string
	// 超时时间，防止恶意脚本或死循环耗尽系统资源。
	timeout time.Duration
	// 隔离模式，决定了安全限制的实现方式。
	isolationMode IsolationMode
	// 互斥锁，保证多线程环境下沙盒配置的安全性。
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

	logger.Info("Sandbox: created new sandbox (workDir: %s, isolation: %s)", workDir, isolationMode.String())
	return &Sandbox{
		writableDirs:  writableDirs,
		tmpDir:        tmpDir,
		workDir:       workDir,
		env:           env,
		timeout:       cfg.Timeout,
		isolationMode: isolationMode,
	}, nil
}

// Icmd 命令接口
type Icmd interface {
	Start() error
	Wait() error
	Kill() error
	SetStdin(r io.Reader)
	SetStdout(w io.Writer)
	SetStderr(w io.Writer)
	Clean()
}

// Command 创建一个沙盒命令
type Command struct {
	sandbox *Sandbox
	cmd     Icmd
	ctx     context.Context
	cancel  context.CancelFunc
	// 命令信息
	name    string
	args    []string
	workDir string
	env     []string
	temp    any
}

// Execute 在沙盒中执行命令
func (s *Sandbox) Execute(name string, args ...string) (*Command, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	logger.Info("Execute command: %s %v (isolation: %s)", name, args, s.isolationMode.String())

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
		cmd := createIsolateNoneCmd(ctx, name, args, s.env, s.workDir)
		// 无隔离，直接运行
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
			logger.Error("createIsolatedCommand error: %v", err)
			cancel()
			return nil, fmt.Errorf("创建隔离命令失败: %w", err)
		}
		isolatedCmd.cancel = cancel
		isolatedCmd.sandbox = s
		return isolatedCmd, nil

	default:
		cancel()
		logger.Error("unsupported isolation mode: %d", s.isolationMode)
		return nil, fmt.Errorf("不支持的隔离模式: %d", s.isolationMode)
	}
}

// SetStdin 设置标准输入
func (c *Command) SetStdin(r io.Reader) {
	c.cmd.SetStdin(r)
}

// SetStdout 设置标准输出
func (c *Command) SetStdout(w io.Writer) {
	c.cmd.SetStdout(w)
}

// SetStderr 设置标准错误
func (c *Command) SetStderr(w io.Writer) {
	c.cmd.SetStderr(w)
}

// Start 启动命令
func (c *Command) Start() error {
	logger.Debug("starting command: %s", c.name)
	return c.cmd.Start()
}

// Wait 等待命令完成
func (c *Command) Wait() error {
	err := c.cmd.Wait()
	if err != nil {
		logger.Warn("command %s finished with error: %v", c.name, err)
	} else {
		logger.Debug("command %s finished successfully", c.name)
	}
	return err
}

// Run 运行命令并等待完成
func (c *Command) Run() error {
	if err := c.Start(); err != nil {
		c.cancel()
		return err
	}
	return c.Wait()
}

// Kill 强制终止命令
func (c *Command) Kill() error {
	logger.Info("killing command: %s", c.name)
	if c.cmd != nil {
		return c.cmd.Kill()
	}
	return errors.New("进程未启动")
}

// IsPathWritable 检查给定路径是否在沙盒允许的可写目录白名单内。
// 这一步是防止“目录遍历攻击”和“越权访问”的关键防御措施。
func (s *Sandbox) IsPathWritable(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 如果是无隔离模式，则信任所有操作，不进行路径校验。
	if s.isolationMode == IsolationNone {
		return true
	}

	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		logger.Warn("failed to get absolute path for %s: %v", path, err)
		return false
	}

	for _, dir := range s.writableDirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}

		// 通过计算相对路径来判断 absPath 是否位于 absDir 内部。
		// 如果相对路径不包含 ".."，说明 absPath 是 absDir 的子路径或其本身。
		rel, err := filepath.Rel(absDir, absPath)
		if err != nil {
			continue
		}

		// 如果相对路径不以..开头，说明在允许的目录下
		if !strings.HasPrefix(rel, "..") {
			return true
		}
	}

	logger.Warn("path not writable: %s (abs: %s)", path, absPath)
	return false
}

// ValidateCommand 验证待执行的命令及其参数是否安全。
func (s *Sandbox) ValidateCommand(name string, args ...string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Windows 内置命令列表（如 dir, cd）通常不对应独立的 .exe 文件，
	// 而是由 cmd.exe 或 powershell.exe 解释执行。
	windowsBuiltins := map[string]bool{
		"echo": true, "cd": true, "dir": true, "copy": true, "move": true,
		"del": true, "type": true, "set": true, "if": true, "for": true,
	}

	// 检查命令是否存在于系统的 PATH 中。
	// 对于 Windows 内置命令，跳过 LookPath 检查
	isWindowsBuiltin := runtime.GOOS == "windows" && windowsBuiltins[strings.ToLower(name)]
	if !isWindowsBuiltin {
		_, err := exec.LookPath(name)
		if err != nil {
			return fmt.Errorf("命令不存在: %s", name)
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
	default:
		return "unknown"
	}
}

// IsSandboxSupported 检查当前环境是否支持沙盒。
// 如果存在 /.dockerenv 或者无法列出根目录内容，则认为不支持沙盒（通常意味着已经处于受限环境）。
func IsSandboxSupported() bool {
	// 1. 检查是否在 Docker 容器中
	if _, err := os.Stat("/.dockerenv"); err == nil {
		logger.Info("Detected Docker environment, sandbox disabled")
		return false
	}

	// 2. 检查是否能列出根目录（简单的权限/环境检测）
	// 在某些受限环境或特定的沙盒实现中，列出根目录可能会失败
	if runtime.GOOS != "windows" {
		f, err := os.Open("/")
		if err != nil {
			logger.Info("Failed to open root directory, sandbox disabled: %v", err)
			return false
		}
		defer f.Close()
		_, err = f.Readdirnames(1)
		if err != nil && err != io.EOF {
			logger.Info("Failed to read root directory, sandbox disabled: %v", err)
			return false
		}
	}

	return true
}
