//go:build windows

package windows

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procLsaOpenPolicy = dllAdvapi.NewProc("LsaOpenPolicy")
var procLsaClose = dllAdvapi.NewProc("LsaClose")
var procLsaAddAccountRights = dllAdvapi.NewProc("LsaAddAccountRights")

// LsaHandle LSA 句柄类型
type LsaHandle uintptr

// lsaUnicodeString LSA Unicode 字符串结构
type lsaUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

// lsaObjectAttributes LSA 对象属性结构
type lsaObjectAttributes struct {
	Length                   uint32
	RootDirectory            uintptr
	ObjectName               *lsaUnicodeString
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

// 初始化 LSA_UNICODE_STRING
func newLSAUnicodeString(s string) lsaUnicodeString {
	if s == "" {
		return lsaUnicodeString{}
	}
	p, _ := syscall.UTF16PtrFromString(s)
	len := uint16(len(s) * 2) // UTF-16 每个字符2字节
	return lsaUnicodeString{
		Length:        len,
		MaximumLength: len + 2,
		Buffer:        p,
	}
}

// LibLsaOpenPolicy 打开一个策略对象
func LibLsaOpenPolicy(systemName string, desiredAccess uint32) (handle LsaHandle, err error) {
	var systemNameUS lsaUnicodeString
	if systemName != "" {
		systemNameUS = newLSAUnicodeString(systemName)
	}

	// 初始化对象属性（全部置零）
	objAttr := lsaObjectAttributes{
		Length: 0, // 必须设置为0
	}

	var policyHandle LsaHandle

	ret, _, _ := procLsaOpenPolicy.Call(
		uintptr(unsafe.Pointer(&systemNameUS)),
		uintptr(unsafe.Pointer(&objAttr)),
		uintptr(desiredAccess),
		uintptr(unsafe.Pointer(&policyHandle)),
	)

	if ret != 0 {
		return 0, fmt.Errorf("LsaOpenPolicy failed: 0x%X", ret)
	}

	return policyHandle, nil
}

// LibLsaClose 关闭 LSA 策略句柄
func LibLsaClose(policyHandle LsaHandle) error {
	ret, _, _ := procLsaClose.Call(uintptr(policyHandle))
	if ret != 0 {
		return fmt.Errorf("LsaClose failed: 0x%X", ret)
	}
	return nil
}

// LibLsaAddAccountRights 添加账户权限
func LibLsaAddAccountRights(policyHandle LsaHandle, sid *windows.SID, privilegeName string) error {
	// 创建 LSA_UNICODE_STRING 数组（单个元素）
	rights := newLSAUnicodeString(privilegeName)

	ret, _, _ := procLsaAddAccountRights.Call(
		uintptr(policyHandle),
		uintptr(unsafe.Pointer(sid)),
		uintptr(unsafe.Pointer(&rights)),
		1, // 权限数量
	)

	if ret != 0 {
		return fmt.Errorf("LsaAddAccountRights failed: 0x%X", ret)
	}

	return nil
}
