//go:build windows

package pty

import (
	"errors"
	"io"
	"os"
	"sync"
)

// PTY 表示一个伪终端主端
// Windows 上使用管道对实现，作为 ConPTY 的轻量替代——直接透传数据，不做终端仿真
type PTY struct {
	readFd  *os.File // 子进程 stdout 读取端
	writeFd *os.File // 子进程 stdin 写入端
	file    *os.File // 对外暴露的文件（读取端）
	mu      sync.Mutex
	closed  bool
	rows    uint16
	cols    uint16
}

// Config PTY配置
type Config struct {
	Rows uint16
	Cols uint16
}

// New 创建一个新的 PTY，使用管道作为 ConPTY 的轻量替代
func New(cfg Config) (*PTY, *os.File, error) {
	if cfg.Rows == 0 {
		cfg.Rows = 24
	}
	if cfg.Cols == 0 {
		cfg.Cols = 80
	}

	// 输出管道：子进程 stdout → 我们读取
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	_ = outW.Close() // 关闭写入端，只保留读取

	// 输入管道：我们写入 → 子进程 stdin
	inR, inW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		return nil, nil, err
	}
	_ = inR.Close() // 关闭读取端，只保留写入

	p := &PTY{
		readFd:  outR,
		writeFd: inW,
		file:    outR,
		rows:    cfg.Rows,
		cols:    cfg.Cols,
	}

	return p, p.file, nil
}

// File 返回对外暴露的文件描述符
func (p *PTY) File() *os.File {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.file
}

// Read 从 PTY 读取子进程输出
func (p *PTY) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, errors.New("PTY closed")
	}
	if p.readFd == nil {
		return 0, errors.New("PTY not initialized")
	}
	return p.readFd.Read(buf)
}

// Write 向 PTY 写入数据（发送到子进程 stdin）
func (p *PTY) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, errors.New("PTY closed")
	}
	if p.writeFd == nil {
		return 0, errors.New("PTY not initialized")
	}
	return p.writeFd.Write(data)
}

// Close 关闭 PTY
func (p *PTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	var errs []error
	if p.readFd != nil {
		if err := p.readFd.Close(); err != nil {
			errs = append(errs, err)
		}
		p.readFd = nil
	}
	if p.writeFd != nil {
		if err := p.writeFd.Close(); err != nil {
			errs = append(errs, err)
		}
		p.writeFd = nil
	}
	p.file = nil

	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// Resize 调整终端大小
// Windows 管道透传下终端大小调整无实际效果，仅记录值
func (p *PTY) Resize(rows, cols uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.New("PTY closed")
	}
	p.rows = rows
	p.cols = cols
	return nil
}

// GetSize 获取终端大小
func (p *PTY) GetSize() (rows, cols int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, 0, errors.New("PTY closed")
	}
	return int(p.rows), int(p.cols), nil
}

// CopyTo 将 PTY 输出复制到 writer
func (p *PTY) CopyTo(w io.Writer) error {
	if p.readFd == nil {
		return errors.New("PTY not initialized")
	}
	_, err := io.Copy(w, p.readFd)
	return err
}

// CopyFrom 从 reader 复制数据到 PTY
func (p *PTY) CopyFrom(r io.Reader) error {
	if p.writeFd == nil {
		return errors.New("PTY not initialized")
	}
	_, err := io.Copy(p.writeFd, r)
	return err
}

// Pipe 创建双向管道
func (p *PTY) Pipe(rw io.ReadWriter) error {
	if p.readFd == nil || p.writeFd == nil {
		return errors.New("PTY not initialized")
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	// 从 PTY 读取并写入到 rw
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(rw, p.readFd)
		if err != nil && err != io.EOF {
			errChan <- err
		}
	}()

	// 从 rw 读取并写入到 PTY
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(p.writeFd, rw)
		if err != nil && err != io.EOF {
			errChan <- err
		}
	}()

	wg.Wait()
	close(errChan)

	for range errChan {
		return errors.New("pipe failed")
	}
	return nil
}

// ReadStderr 从 stderr 读取数据（如果支持）
// 管道透传下 stdout 和 stderr 合并输出
func (p *PTY) ReadStderr(buf []byte) (int, error) {
	// 管道通常将 stdout 和 stderr 统一处理
	return p.Read(buf)
}
