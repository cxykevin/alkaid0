//go:build windows

package actions

import (
	"io/fs"
	"os/user"
)

// ---- Permissions helper ----

// getPermissions 获取文件权限字符串（八进制格式）
func getPermissions(info fs.FileInfo) string {
	if info.Mode().Perm()&0200 == 0 {
		return "0555"
	}
	return "0755"
}

// ---- Ownership helper (platform-specific) ----

// getOwner 获取文件所有者的用户名
func getOwner(_ fs.FileInfo) string {
	usr, err := user.Current()
	if err != nil {
		return "(unknown)"
	}
	if usr.Username == "SYSTEM" || usr.Username == "NT AUTHORITY\\SYSTEM" || usr.Username == "LocalSystem" {
		return "root"
	}
	return usr.Username
}
