//go:build windows

package pty

import (
	"errors"
	"io"
	"os"
	"sync"
)

// PTY 表示一个伪终端主端（仅文件描述符）
type PTY struct {
	fd     *os.File
	mu     sync.Mutex
	closed bool
	rows   uint16
	cols   uint16
	con    *conPty
}

// Config PTY配置
type Config struct {
	// 终端大小
	Rows uint16
	Cols uint16
}

// New 创建一个新的PTY，仅打开伪终端主端文件描述符
func New(cfg Config) (*PTY, *os.File, error) {
	// 设置默认终端大小
	if cfg.Rows == 0 {
		cfg.Rows = 24
	}
	if cfg.Cols == 0 {
		cfg.Cols = 80
	}

	master, slave, con, err := openPTY(cfg)
	if err != nil {
		return nil, nil, err
	}
	if slave != nil {
		_ = slave.Close()
	}

	p := &PTY{
		fd:     master,
		closed: false,
		rows:   cfg.Rows,
		cols:   cfg.Cols,
		con:    con,
	}

	if cfg.Rows > 0 && cfg.Cols > 0 {
		_ = setWinsize(int(master.Fd()), int(cfg.Cols), int(cfg.Rows), con)
	}

	return p, master, nil
}

// File 返回底层伪终端主端文件描述符
func (p *PTY) File() *os.File {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fd
}

// Read 从PTY读取数据
func (p *PTY) Read(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, errors.New("PTY closed")
	}
	if p.fd == nil {
		return 0, errors.New("PTY not initialized")
	}
	return p.fd.Read(buf)
}

// Write 向PTY写入数据
func (p *PTY) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, errors.New("PTY closed")
	}
	if p.fd == nil {
		return 0, errors.New("PTY not initialized")
	}
	return p.fd.Write(data)
}

// Close 关闭PTY
func (p *PTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	if p.fd != nil {
		_ = p.fd.Close()
		p.fd = nil
	}
	if p.con != nil {
		_ = p.con.Close()
		p.con = nil
	}

	return nil
}

// Resize 调整终端大小
func (p *PTY) Resize(rows, cols uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.New("PTY closed")
	}
	if p.fd == nil {
		return errors.New("PTY not initialized")
	}

	if err := setWinsize(int(p.fd.Fd()), int(cols), int(rows), p.con); err != nil {
		return err
	}
	p.rows = rows
	p.cols = cols
	return nil
}

// CopyTo 将PTY输出复制到writer
func (p *PTY) CopyTo(w io.Writer) error {
	if p.fd == nil {
		return errors.New("PTY not initialized")
	}
	_, err := io.Copy(w, p.fd)
	return err
}

// CopyFrom 从reader复制数据到PTY
func (p *PTY) CopyFrom(r io.Reader) error {
	if p.fd == nil {
		return errors.New("PTY not initialized")
	}
	_, err := io.Copy(p.fd, r)
	return err
}

// Pipe 创建双向管道
func (p *PTY) Pipe(rw io.ReadWriter) error {
	if p.fd == nil {
		return errors.New("PTY not initialized")
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	// 从PTY读取并写入到rw
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(rw, p.fd)
		if err != nil && err != io.EOF {
			errChan <- err
		}
	}()

	// 从rw读取并写入到PTY
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := io.Copy(p.fd, rw)
		if err != nil && err != io.EOF {
			errChan <- err
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
		return errors.New("pipe failed")
	}

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

// ReadStderr 从stderr读取数据（如果支持）
// 注意：真正的PTY通常将stdout和stderr合并
func (p *PTY) ReadStderr(buf []byte) (int, error) {
	// PTY通常将stdout和stderr合并到一个流
	// 这里为了兼容性保留此方法，但实际上读取的是合并后的输出
	return p.Read(buf)
}
