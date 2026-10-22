//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package cronjob

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// fileLock 进程级独占锁（flock(LOCK_EX|LOCK_NB)）。
//
// 锁由打开的文件描述符持有：进程退出（包括 os.Exit / 崩溃）时内核自动释放，
// 不会留下需要手工清理的陈旧锁；锁文件本身可以长期存在。
type fileLock struct {
	f *os.File
}

// acquireFileLock 非阻塞获取独占锁；已被其它进程持有时返回 ErrLocked。
func acquireFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// release 释放锁并关闭文件。
func (l *fileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
