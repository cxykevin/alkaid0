//go:build windows

package windows

import (
	"golang.org/x/sys/windows"
)

var (
	dllNetapi  = windows.NewLazySystemDLL("netapi32.dll")
	dllAdvapi  = windows.NewLazySystemDLL("advapi32.dll")
	dllCrypt32 = windows.NewLazySystemDLL("crypt32.dll")
	dllKernel32 = windows.NewLazySystemDLL("kernel32.dll")
)
