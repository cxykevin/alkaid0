//go:build windows

package cronjob

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// fileLock 进程级独占锁（LockFileEx + LOCKFILE_FAIL_IMMEDIATELY）。
//
// 句柄关闭或进程退出时 Windows 自动释放锁，不会留下陈旧锁。
type fileLock struct {
	f *os.File
}

// acquireFileLock 非阻塞获取独占锁；已被其它进程持有时返回 ErrLocked。
func acquireFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ol := new(windows.Overlapped)
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// release 释放锁并关闭句柄。
func (l *fileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, new(windows.Overlapped))
	_ = l.f.Close()
	l.f = nil
}
