//go:build !windows

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
	slave  *os.File // 保持 slave 端打开，防止 macOS 写入主端时返回 EIO
	mu     sync.Mutex
	closed bool
	rows   uint16
	cols   uint16
}

// Config PTY配置
type Config struct {
	// 终端大小
	Rows uint16
	Cols uint16
}

// setWinsizeFile 设置终端大小，且不把文件切回阻塞模式。
// 不能直接用 File.Fd()：对由 Go 自己设为非阻塞的 fd，Fd() 会把它切回阻塞模式
// （见 os.File.fd），从而失去 poller 的可取消读能力——命令留下持有从端的后代进程时，
// 读取协程将无法被 Close 唤醒。
func setWinsizeFile(f *os.File, cols, rows int) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ierr error
	if cerr := rc.Control(func(fd uintptr) { ierr = setWinsize(int(fd), cols, rows) }); cerr != nil {
		return cerr
	}
	return ierr
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

	master, slave, err := openPTY()
	if err != nil {
		return nil, nil, err
	}

	p := &PTY{
		fd:     master,
		slave:  slave, // 保持 slave 打开，避免写入 master 时 EIO
		closed: false,
		rows:   cfg.Rows,
		cols:   cfg.Cols,
	}

	if cfg.Rows > 0 && cfg.Cols > 0 {
		_ = setWinsizeFile(master, int(cfg.Cols), int(cfg.Rows))
	}

	return p, master, nil
}

// Open 打开 PTY 并返回主端和从端文件描述符，不关闭从端。
// 主端用于父进程读写，从端用于挂载到子进程的标准输入/输出/错误。
// 调用方负责关闭 master 和 slave。
func Open(cfg Config) (master, slave *os.File, err error) {
	if cfg.Rows == 0 {
		cfg.Rows = 24
	}
	if cfg.Cols == 0 {
		cfg.Cols = 80
	}
	master, slave, err = openPTY()
	if err != nil {
		return nil, nil, err
	}
	if cfg.Rows > 0 && cfg.Cols > 0 {
		_ = setWinsizeFile(master, int(cfg.Cols), int(cfg.Rows))
	}
	return master, slave, nil
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
	if p.closed {
		p.mu.Unlock()
		return 0, errors.New("PTY closed")
	}
	if p.fd == nil {
		p.mu.Unlock()
		return 0, errors.New("PTY not initialized")
	}
	fd := p.fd
	p.mu.Unlock()

	// 锁外执行阻塞读取：否则空闲时阻塞在 fd.Read 会与并发 Close 持锁互相死锁
	return fd.Read(buf)
}

// Write 向PTY写入数据
func (p *PTY) Write(data []byte) (int, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, errors.New("PTY closed")
	}
	if p.fd == nil {
		p.mu.Unlock()
		return 0, errors.New("PTY not initialized")
	}
	fd := p.fd
	p.mu.Unlock()

	// 锁外执行写入，避免管道写满时持锁阻塞与 Close 死锁
	return fd.Write(data)
}

// Close 关闭PTY
func (p *PTY) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}
	p.closed = true

	if p.slave != nil {
		_ = p.slave.Close()
		p.slave = nil
	}

	if p.fd != nil {
		return p.fd.Close()
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

	if err := setWinsizeFile(p.fd, int(cols), int(rows)); err != nil {
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
	wg.Go(func() {
		_, err := io.Copy(rw, p.fd)
		if err != nil && err != io.EOF {
			errChan <- err
		}
	})

	// 从rw读取并写入到PTY
	wg.Go(func() {
		_, err := io.Copy(p.fd, rw)
		if err != nil && err != io.EOF {
			errChan <- err
		}
	})

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
