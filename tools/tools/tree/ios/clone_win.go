//go:build windows

package ios

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type duplicateExtentsData struct {
	FileHandle       windows.Handle
	SourceFileOffset int64
	TargetFileOffset int64
	ByteCount        int64
}

func cloneFile(srcFd, dstFd int) error {
	// 先拿到两个句柄的 size
	var srcInfo, dstInfo windows.ByHandleFileInformation
	srcH := windows.Handle(srcFd)
	dstH := windows.Handle(dstFd)

	if err := windows.GetFileInformationByHandle(srcH, &srcInfo); err != nil {
		return err
	}
	if err := windows.GetFileInformationByHandle(dstH, &dstInfo); err != nil {
		return err
	}
	size := int64(srcInfo.FileSizeHigh)<<32 + int64(srcInfo.FileSizeLow)

	// FSCTL_DUPLICATE_EXTENTS_TO_FILE 要求 64 k 对齐，这里直接按 1 个整块克隆
	dup := duplicateExtentsData{
		FileHandle:       srcH,
		SourceFileOffset: 0,
		TargetFileOffset: 0,
		ByteCount:        size,
	}
	var bytesReturned uint32
	return windows.DeviceIoControl(
		dstH,
		windows.FSCTL_DUPLICATE_EXTENTS_TO_FILE,
		(*byte)(unsafe.Pointer(&dup)),
		uint32(unsafe.Sizeof(dup)),
		nil,
		0,
		&bytesReturned,
		nil,
	)
}
