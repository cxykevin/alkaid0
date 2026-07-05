//go:build !windows

package actions

import (
	"fmt"
	"io/fs"
	"os/user"
	"syscall"
)

// getOwnerUnix 在 Unix 系统上通过 syscall.Stat_t 获取文件所有者用户名
func getOwnerUnix(info fs.FileInfo) string {
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
