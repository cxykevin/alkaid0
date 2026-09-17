//go:build linux

// Package pty 实现 Linux 平台的伪终端 (PTY) 创建与窗口大小设置
package pty

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY 在 Linux 上打开一个伪终端，返回 master 和 slave 端文件描述符
func openPTY() (*os.File, *os.File, error) {
	// master 必须以非阻塞方式打开：os.NewFile 检测到 O_NONBLOCK 后会把它交给 runtime
	// poller 管理，Close/SetReadDeadline 才能取消挂起的 Read。
	// 阻塞 fd 的 Close 无法唤醒阻塞中的 read：命令留下仍持有从端的后代进程时
	// （background 场景常见），读取协程会永远卡住，任务停在 running。
	masterFd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}

	var ptn uint32
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(masterFd), unix.TIOCGPTN, uintptr(unsafe.Pointer(&ptn)))
	if errno != 0 {
		err = errno
		_ = unix.Close(masterFd)
		return nil, nil, fmt.Errorf("ioctl(TIOCGPTN): %w", err)
	}
	slaveName := fmt.Sprintf("/dev/pts/%d", ptn)

	var p int
	_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(masterFd), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&p)))
	if errno != 0 {
		err = errno
		_ = unix.Close(masterFd)
		return nil, nil, fmt.Errorf("ioctl(TIOCSPTLCK): %w", err)
	}

	slaveFd, err := unix.Open(slaveName, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(masterFd)
		return nil, nil, err
	}

	return os.NewFile(uintptr(masterFd), "pty-master"), os.NewFile(uintptr(slaveFd), "pty-slave"), nil
}

func setWinsize(fd int, cols, rows int) error {
	ws := &unix.Winsize{Col: uint16(cols), Row: uint16(rows)}
	return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
}
