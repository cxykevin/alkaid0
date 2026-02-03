package pty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/KennethanCeyer/ptyx"
)

// PTY 表示一个伪终端
type PTY struct {
	session ptyx.Session
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	config  Config
}

// Config PTY配置
type Config struct {
	// 命令名称
	Command string
	// 命令参数
	Args []string
	// 工作目录
	Dir string
	// 环境变量
	Env []string
	// 终端大小
	Rows uint16
	Cols uint16
}

// New 创建一个新的PTY
// 使用ptyx库实现真正的跨平台PTY
func New(cfg Config) (*PTY, error) {
	if cfg.Command == "" {
		// 根据平台选择默认shell
		switch runtime.GOOS {
		case "windows":
			cfg.Command = "cmd.exe"
		default:
			cfg.Command = "/bin/sh"
		}
	}

	// 设置默认终端大小
	if cfg.Rows == 0 {
		cfg.Rows = 24
	}
	if cfg.Cols == 0 {
		cfg.Cols = 80
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &PTY{
		ctx:    ctx,
		cancel: cancel,
		closed: false,
		config: cfg,
	}, nil
}

// Start 启动PTY
func (p *PTY) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.New("PTY已关闭")
	}

	// 创建ptyx spawn选项
	opts := ptyx.SpawnOpts{
		Prog: p.config.Command,
		Args: p.config.Args,
		Dir:  p.config.Dir,
		Env:  p.config.Env,
		Rows: int(p.config.Rows),
		Cols: int(p.config.Cols),
	}

	// 启动会话
	session, err := ptyx.Spawn(p.ctx, opts)
	if err != nil {
		return fmt.Errorf("启动PTY会话失败: %w", err)
	}

	p.session = session
	return nil
}

// Wait 等待PTY进程结束
func (p *PTY) Wait() error {
	if p.session == nil {
		return errors.New("PTY未启动")
	}
	return p.session.Wait()
}

// Read 从PTY读取数据
func (p *PTY) Read(buf []byte) (int, error) {
	if p.session == nil {
		return 0, errors.New("PTY未启动")
	}
	return p.session.PtyReader().Read(buf)
}

// Write 向PTY写入数据
func (p *PTY) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, errors.New("PTY已关闭")
	}

	if p.session == nil {
		return 0, errors.New("PTY未启动")
	}

	return p.session.PtyWriter().Write(data)
}

// Close 关闭PTY
func (p *PTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	p.closed = true
	p.cancel()

	if p.session != nil {
		return p.session.Close()
	}

	return nil
}

// Resize 调整终端大小
func (p *PTY) Resize(rows, cols uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.New("PTY已关闭")
	}

	if p.session == nil {
		return errors.New("PTY未启动")
	}

	return p.session.Resize(int(cols), int(rows))
}

// GetPID 获取进程ID
func (p *PTY) GetPID() int {
	if p.session == nil {
		return -1
	}
	return p.session.Pid()
}

// IsRunning 检查进程是否在运行
func (p *PTY) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || p.session == nil {
		return false
	}

	// 检查进程是否存在
	pid := p.session.Pid()
	if pid <= 0 {
		return false
	}

	// 尝试发送信号0检查进程是否存在
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(os.Signal(nil))
	return err == nil
}

// CopyTo 将PTY输出复制到writer
func (p *PTY) CopyTo(w io.Writer) error {
	if p.session == nil {
		return errors.New("PTY未启动")
	}
	_, err := io.Copy(w, p.session.PtyReader())
	return err
}

// CopyFrom 从reader复制数据到PTY
func (p *PTY) CopyFrom(r io.Reader) error {
	if p.session == nil {
		return errors.New("PTY未启动")
	}
	_, err := io.Copy(p.session.PtyWriter(), r)
	return err
}

// Pipe 创建双向管道
func (p *PTY) Pipe(rw io.ReadWriter) error {
	if p.session == nil {
		return errors.New("PTY未启动")
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	// 从PTY读取并写入到rw
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(rw, p.session.PtyReader())
		if err != nil && err != io.EOF {
			errChan <- fmt.Errorf("PTY->RW复制错误: %w", err)
		}
	}()

	// 从rw读取并写入到PTY
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(p.session.PtyWriter(), rw)
		if err != nil && err != io.EOF {
			errChan <- fmt.Errorf("RW->PTY复制错误: %w", err)
		}
	}()

	wg.Wait()
	close(errChan)

	// 收集错误
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("管道错误: %v", errs)
	}

	return nil
}

// GetSize 获取终端大小
func (p *PTY) GetSize() (rows, cols int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, 0, errors.New("PTY已关闭")
	}

	// 返回配置的大小
	return int(p.config.Rows), int(p.config.Cols), nil
}

// ReadStderr 从stderr读取数据（如果支持）
// 注意：真正的PTY通常将stdout和stderr合并
func (p *PTY) ReadStderr(buf []byte) (int, error) {
	// PTY通常将stdout和stderr合并到一个流
	// 这里为了兼容性保留此方法，但实际上读取的是合并后的输出
	return p.Read(buf)
}

// GetSession 获取底层ptyx会话（高级用法）
func (p *PTY) GetSession() ptyx.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session
}

// Kill 强制终止进程
func (p *PTY) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.session == nil {
		return errors.New("PTY未启动")
	}

	return p.session.Kill()
}
