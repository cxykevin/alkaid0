//go:build !windows

package actions

import (
	"fmt"
	"io/fs"
	"os/user"
	"syscall"
)

// ---- Permissions helper ----

// getPermissions 获取文件权限字符串（八进制格式）
func getPermissions(info fs.FileInfo) string {
	return fmt.Sprintf("%o", info.Mode().Perm())
}

// ---- Ownership helper ----

func getOwnerCurrentUser() string {
	usr, err := user.Current()
	if err != nil {
		return "(unknown)"
	}
	return usr.Username
}

// getOwner 获取文件所有者的用户名
func getOwner(info fs.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return getOwnerCurrentUser()
	}
	usr, err := user.LookupId(fmt.Sprintf("%d", stat.Uid))
	if err != nil {
		return fmt.Sprintf("%d", stat.Uid)
	}
	return usr.Username
}
