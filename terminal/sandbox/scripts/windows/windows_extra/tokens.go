//go:build windows

package windows

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Security*RequiredPrivilegeRID 完整性
const (
	SecurityMandatoryLowRID    uint32 = 0x1000
	SecurityMandatoryMediumRID uint32 = 0x2000
	SecurityMandatoryHighRID   uint32 = 0x4000
)

var procCreateRestrictedToken = dllAdvapi.NewProc("CreateRestrictedToken")

// LibCreateRestrictedTokenFlags 创建受限令牌的标志
type LibCreateRestrictedTokenFlags uint32

// 常用标志常量
const (
	LibCreateRestrictedTokenFlagsDisableMaxPrivilege LibCreateRestrictedTokenFlags = 0x1 // 禁用所有特权，除了 SeChangeNotifyPrivilege
	LibCreateRestrictedTokenFlagsSandboxInert        LibCreateRestrictedTokenFlags = 0x2 // 不检查 AppLocker 规则
	LibCreateRestrictedTokenFlagsLUAToken            LibCreateRestrictedTokenFlags = 0x4 // 创建 LUA 令牌
	LibCreateRestrictedTokenFlagsWriteRestricted     LibCreateRestrictedTokenFlags = 0x8 // 限制 SID 仅用于写访问检查
)

// LibCreateRestrictedToken 创建一个新的受限访问令牌
// Windows API CreateRestrictedToken 的 Go 绑定
func LibCreateRestrictedToken(
	existingTokenHandle windows.Token,
	flags LibCreateRestrictedTokenFlags,
	disableSidCount uint32,
	sidsToDisable *windows.SIDAndAttributes,
	deletePrivilegeCount uint32,
	privilegesToDelete *windows.LUIDAndAttributes,
	restrictedSidCount uint32,
	sidsToRestrict *windows.SIDAndAttributes,
) (windows.Token, error) {

	var newTokenHandle windows.Handle

	ret, _, err := procCreateRestrictedToken.Call(
		uintptr(existingTokenHandle),
		uintptr(uint32(flags)),
		uintptr(disableSidCount),
		uintptr(unsafe.Pointer(sidsToDisable)),
		uintptr(deletePrivilegeCount),
		uintptr(unsafe.Pointer(privilegesToDelete)),
		uintptr(restrictedSidCount),
		uintptr(unsafe.Pointer(sidsToRestrict)),
		uintptr(unsafe.Pointer(&newTokenHandle)),
	)

	if ret == 0 {
		return 0, err
	}

	return windows.Token(newTokenHandle), nil
}
