//go:build windows

package pty

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type conPty struct {
	hpc          *windows.Handle
	inRHostWrite windows.Handle
	outRHostRead windows.Handle
	inFile       *os.File
	outFile      *os.File
	attrList     *windows.ProcThreadAttributeListContainer
	size         windows.Coord
	closeOnce    sync.Once
}

func newConPty(cols, rows int, flags uint32) (*conPty, error) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 25
	}

	c := &conPty{
		hpc:  new(windows.Handle),
		size: windows.Coord{X: int16(cols), Y: int16(rows)},
	}

	var ptyInRead, ptyOutWrite windows.Handle
	if err := windows.CreatePipe(&ptyInRead, &c.inRHostWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("failed to create input pipe for pseudo console: %w", err)
	}
	if err := windows.CreatePipe(&c.outRHostRead, &ptyOutWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(c.inRHostWrite)
		return nil, fmt.Errorf("failed to create output pipe for pseudo console: %w", err)
	}
	if err := windows.SetHandleInformation(c.inRHostWrite, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(c.inRHostWrite)
		_ = windows.CloseHandle(c.outRHostRead)
		_ = windows.CloseHandle(ptyOutWrite)
		return nil, fmt.Errorf("failed to set handle information for input pipe: %w", err)
	}
	if err := windows.SetHandleInformation(c.outRHostRead, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(c.inRHostWrite)
		_ = windows.CloseHandle(c.outRHostRead)
		_ = windows.CloseHandle(ptyOutWrite)
		return nil, fmt.Errorf("failed to set handle information for output pipe: %w", err)
	}
	if err := windows.CreatePseudoConsole(c.size, ptyInRead, ptyOutWrite, flags, c.hpc); err != nil {
		_ = windows.CloseHandle(ptyInRead)
		_ = windows.CloseHandle(c.inRHostWrite)
		_ = windows.CloseHandle(c.outRHostRead)
		_ = windows.CloseHandle(ptyOutWrite)
		return nil, fmt.Errorf("failed to create pseudo console: %w", err)
	}
	if err := windows.CloseHandle(ptyInRead); err != nil {
		windows.ClosePseudoConsole(*c.hpc)
		_ = windows.CloseHandle(c.inRHostWrite)
		_ = windows.CloseHandle(c.outRHostRead)
		_ = windows.CloseHandle(ptyOutWrite)
		return nil, fmt.Errorf("failed to close pseudo console handle: %w", err)
	}
	if err := windows.CloseHandle(ptyOutWrite); err != nil {
		windows.ClosePseudoConsole(*c.hpc)
		_ = windows.CloseHandle(c.inRHostWrite)
		_ = windows.CloseHandle(c.outRHostRead)
		return nil, fmt.Errorf("failed to close pseudo console handle: %w", err)
	}

	c.inFile = os.NewFile(uintptr(c.inRHostWrite), "conpty-stdin")
	c.outFile = os.NewFile(uintptr(c.outRHostRead), "conpty-stdout")

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(*c.hpc)
		_ = c.inFile.Close()
		_ = c.outFile.Close()
		return nil, fmt.Errorf("failed to create proc thread attribute list: %w", err)
	}
	if err := attrList.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		unsafe.Pointer(*c.hpc),
		unsafe.Sizeof(*c.hpc),
	); err != nil {
		attrList.Delete()
		windows.ClosePseudoConsole(*c.hpc)
		_ = c.inFile.Close()
		_ = c.outFile.Close()
		return nil, fmt.Errorf("failed to update proc thread attributes: %w", err)
	}
	c.attrList = attrList

	return c, nil
}

func (c *conPty) resize(cols, rows int) error {
	if c == nil {
		return nil
	}
	c.size = windows.Coord{X: int16(cols), Y: int16(rows)}
	return windows.ResizePseudoConsole(*c.hpc, c.size)
}

func (c *conPty) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		if c.hpc != nil && *c.hpc != 0 {
			windows.ClosePseudoConsole(*c.hpc)
		}
		if c.attrList != nil {
			c.attrList.Delete()
			c.attrList = nil
		}
		if c.inFile != nil {
			_ = c.inFile.Close()
			c.inFile = nil
		}
		if c.outFile != nil {
			_ = c.outFile.Close()
			c.outFile = nil
		}
	})
	return nil
}

func openPTY(cfg Config) (*os.File, *os.File, *conPty, error) {
	con, err := newConPty(int(cfg.Cols), int(cfg.Rows), 0)
	if err != nil {
		return nil, nil, nil, err
	}
	return con.outFile, con.inFile, con, nil
}

func setWinsize(fd int, cols, rows int, con *conPty) error {
	if con == nil {
		return nil
	}
	return con.resize(cols, rows)
}
