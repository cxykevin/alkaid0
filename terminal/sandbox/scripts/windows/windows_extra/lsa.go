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

// statusNoneMapped 对应 NTSTATUS STATUS_NONE_MAPPED (0xC0000060)：
// LsaAddAccountRights/LsaEnumerateAccountsWithUserRight 在"权限名不存在"或
// "SID 不在 LSA 中"时都会返回它。
const statusNoneMapped = 0xC0000060

var procLsaEnumerateAccountsWithUserRight = dllAdvapi.NewProc("LsaEnumerateAccountsWithUserRight")

// LibLsaValidateAccountRight 校验一个权限名（privilege 或 account right，
// 如 SeBatchLogonRight）是否被本机 LSA 认可。
//
// LsaEnumerateAccountsWithUserRight 是只读查询：合法名字返回 STATUS_SUCCESS，
// 拼错的名字返回 STATUS_NONE_MAPPED(0xC0000060)，且不会修改任何账户。
// 该 API 能同时覆盖 privilege 与 account right 两类名字——LookupPrivilegeValue
// 解析不了 SeBatchLogonRight 这类登录权限（它们不是 privilege）。
func LibLsaValidateAccountRight(name string) error {
	policyHandle, err := LibLsaOpenPolicy("", LsaPolicyViewLocalInfomation|LsaPolicyLookupNames)
	if err != nil {
		return err
	}
	defer LibLsaClose(policyHandle)

	right := newLSAUnicodeString(name)
	var buf uintptr
	var count uint32
	ret, _, _ := procLsaEnumerateAccountsWithUserRight.Call(
		uintptr(policyHandle),
		uintptr(unsafe.Pointer(&right)),
		uintptr(unsafe.Pointer(&buf)),
		uintptr(unsafe.Pointer(&count)),
	)
	if buf != 0 {
		procLocalFree.Call(buf)
	}
	if ret != 0 {
		if uint32(ret) == statusNoneMapped {
			return fmt.Errorf("未知的权限名 %q (STATUS_NONE_MAPPED)", name)
		}
		return fmt.Errorf("校验权限名 %q 失败: 0x%X", name, ret)
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
		// 把权限名带进错误信息：0xC0000060 最常见的成因就是权限名拼写错误
		// （曾把 SeIncreaseQuotaPrivilege 写成 SeIncraseQuotaPrivilege，
		// 使整个 Windows 沙盒初始化失败，而日志里只有一个十六进制错误码）。
		if uint32(ret) == statusNoneMapped {
			return fmt.Errorf("LsaAddAccountRights(%q) failed: 0x%X (STATUS_NONE_MAPPED：权限名拼写错误或 SID 不存在)", privilegeName, ret)
		}
		return fmt.Errorf("LsaAddAccountRights(%q) failed: 0x%X", privilegeName, ret)
	}

	return nil
}
