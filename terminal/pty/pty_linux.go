//go:build linux

package pty

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openPTY() (*os.File, *os.File, error) {
	masterFd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_CLOEXEC, 0)
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
