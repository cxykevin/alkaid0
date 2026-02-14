//go:build windows

package windows

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procCreateProcessWithLogonW = dllAdvapi.NewProc("CreateProcessWithLogonW")
	procLogonUserW              = dllAdvapi.NewProc("LogonUserW")
	// dllKernel32                 = windows.NewLazyDLL("kernel32.dll")
	// procCloseHandle             = dllKernel32.NewProc("CloseHandle")
)

// 登录标志
const (
	LogonWithProfile = 0x00000001
	LogonNetCertOnly = 0x00000002
)

// 创建标志
const (
	CreateDefaultErrorMode   = 0x04000000
	CreateNewConsole         = 0x00000010
	CreateNewProcessGroup    = 0x00000200
	CreateUnicodeEnvironment = 0x00000400
)

// StartupInfo 结构体对应 Windows 的 STARTUPINFOW
type StartupInfo struct {
	Cb              uint32
	Reserved        *uint16
	Desktop         *uint16
	Title           *uint16
	DwX             uint32
	DwY             uint32
	DwXSize         uint32
	DwYSize         uint32
	DwXCountChars   uint32
	DwYCountChars   uint32
	DwFillAttribute uint32
	DwFlags         uint32
	WShowWindow     uint16
	CbReserved2     uint16
	Reserved2       *byte
	HStdInput       windows.Handle
	HStdOutput      windows.Handle
	HStdError       windows.Handle
}

// ProcessInformation 结构体对应 Windows 的 PROCESS_INFORMATION
type ProcessInformation struct {
	HProcess    windows.Handle
	HThread     windows.Handle
	DwProcessID uint32
	DwThreadID  uint32
}

// LibCreateProcessWithLogonW 使用指定用户凭据创建进程
func LibCreateProcessWithLogonW(
	username string,
	domain string,
	password string,
	logonFlags uint32,
	applicationName string,
	commandLine string,
	creationFlags uint32,
	environment uintptr,
	currentDirectory string,
	startupInfo *StartupInfo,
	processInfo *ProcessInformation,
) error {
	// 转换为 UTF16 指针
	pUsername, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return err
	}

	pDomain, err := windows.UTF16PtrFromString(domain)
	if err != nil {
		return err
	}

	pPassword, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return err
	}

	var pApplicationName *uint16
	if applicationName != "" {
		pApplicationName, err = windows.UTF16PtrFromString(applicationName)
		if err != nil {
			return err
		}
	}

	var pCommandLine *uint16
	if commandLine != "" {
		pCommandLine, err = windows.UTF16PtrFromString(commandLine)
		if err != nil {
			return err
		}
	}

	var pCurrentDirectory *uint16
	if currentDirectory != "" {
		pCurrentDirectory, err = windows.UTF16PtrFromString(currentDirectory)
		if err != nil {
			return err
		}
	}

	ret, _, err := procCreateProcessWithLogonW.Call(
		uintptr(unsafe.Pointer(pUsername)),
		uintptr(unsafe.Pointer(pDomain)),
		uintptr(unsafe.Pointer(pPassword)),
		uintptr(logonFlags),
		uintptr(unsafe.Pointer(pApplicationName)),
		uintptr(unsafe.Pointer(pCommandLine)),
		uintptr(creationFlags),
		environment,
		uintptr(unsafe.Pointer(pCurrentDirectory)),
		uintptr(unsafe.Pointer(startupInfo)),
		uintptr(unsafe.Pointer(processInfo)),
	)

	if ret == 0 {
		return err
	}

	return nil
}

// LibLogonUserW 使用指定用户凭据登录
func LibLogonUserW(username, domain, password string, loginType, loginProvier uint32) (*windows.Token, error) {
	pUsername, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return nil, err
	}
	pDomain, err := windows.UTF16PtrFromString(domain)
	if err != nil {
		return nil, err
	}
	pPassword, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return nil, err
	}
	var tkn windows.Token
	ret, _, err := procLogonUserW.Call(
		uintptr(unsafe.Pointer(pUsername)),
		uintptr(unsafe.Pointer(pDomain)),
		uintptr(unsafe.Pointer(pPassword)),
		uintptr(loginType),
		uintptr(loginProvier),
		uintptr(unsafe.Pointer(&tkn)),
	)
	if ret == 0 {
		return nil, err
	}
	return &tkn, nil
}
