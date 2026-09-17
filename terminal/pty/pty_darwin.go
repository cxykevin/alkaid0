//go:build darwin

package pty

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openPTY() (ptyFile, ttyFile *os.File, err error) {
	// master 以非阻塞方式打开（syscall.O_NONBLOCK）：os.OpenFile 会把 fd 交给 runtime
	// poller 管理，Close/SetReadDeadline 才能取消挂起的 Read；否则命令留下持有从端的
	// 后代进程时读取协程会永远卡住，任务停在 running。
	ptyFile, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}

	defer func() {
		if err != nil {
			_ = ptyFile.Close()
		}
	}()

	if err = ioctl(ptyFile.Fd(), unix.TIOCPTYGRANT, 0); err != nil {
		return nil, nil, fmt.Errorf("ioctl(TIOCPTYGRANT): %w", err)
	}

	if err = ioctl(ptyFile.Fd(), unix.TIOCPTYUNLK, 0); err != nil {
		return nil, nil, fmt.Errorf("ioctl(TIOCPTYUNLK): %w", err)
	}

	snameBuf := make([]byte, 128)
	if err = ioctl(ptyFile.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&snameBuf[0]))); err != nil {
		return nil, nil, fmt.Errorf("ioctl(TIOCPTYGNAME): %w", err)
	}
	sname := string(snameBuf[:clen(snameBuf)])

	ttyFile, err = os.OpenFile(sname, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}

	return ptyFile, ttyFile, nil
}

func setWinsize(fd int, cols, rows int) error {
	ws := &unix.Winsize{Col: uint16(cols), Row: uint16(rows)}
	return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
}

func clen(b []byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == 0 {
			return i
		}
	}
	return len(b)
}

func ioctl(fd, op, arg uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, op, arg)
	if errno != 0 {
		return errno
	}
	return nil
}
