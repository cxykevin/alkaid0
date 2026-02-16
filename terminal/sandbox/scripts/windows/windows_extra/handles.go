package windows

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procInitializeProcThreadAttributeList = dllKernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute         = dllKernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList     = dllKernel32.NewProc("DeleteProcThreadAttributeList")
)

// DuplicateHandleWithWriteDac 复制 handle 并添加 WRITE_DAC 权限
func DuplicateHandleWithWriteDac(handle windows.Handle) (windows.Handle, error) {
	var newHandle windows.Handle
	err := windows.DuplicateHandle(
		windows.CurrentProcess(),
		handle,
		windows.CurrentProcess(),
		&newHandle,
		windows.WRITE_DAC|windows.READ_CONTROL,
		false,
		0,
	)
	if err != nil {
		return 0, err
	}
	return newHandle, nil
}

// LibInitializeProcThreadAttributeList 初始化进程线程属性
func LibInitializeProcThreadAttributeList(lpAttributeList *byte, dwAttributeCount uint32, dwFlags uint32, lpSize *uintptr) error {
	ret, _, err := procInitializeProcThreadAttributeList.Call(
		uintptr(unsafe.Pointer(lpAttributeList)),
		uintptr(dwAttributeCount),
		uintptr(dwFlags),
		uintptr(unsafe.Pointer(lpSize)),
	)
	if ret == 0 {
		return err
	}
	return nil
}

// LibUpdateProcThreadAttribute 更新进程线程属性
func LibUpdateProcThreadAttribute(
	lpAttributeList *byte,
	dwFlags uint32,
	Attribute uintptr,
	lpValue unsafe.Pointer,
	cbSize uintptr,
	lpPreviousValue unsafe.Pointer,
	lpReturnSize *uintptr,
) error {
	ret, _, err := procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(lpAttributeList)),
		uintptr(dwFlags),
		Attribute,
		uintptr(lpValue),
		cbSize,
		uintptr(lpPreviousValue),
		uintptr(unsafe.Pointer(lpReturnSize)),
	)
	if ret == 0 {
		return err
	}
	return nil
}

// LibDeleteProcThreadAttributeList 删除进程线程属性
func LibDeleteProcThreadAttributeList(lpAttributeList *byte) {
	procDeleteProcThreadAttributeList.Call(uintptr(unsafe.Pointer(lpAttributeList)))
}
