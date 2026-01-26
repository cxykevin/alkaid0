//go:build darwin

package ios

import "golang.org/x/sys/unix"

func cloneFile(srcFd, dstFd int) error {
	return unix.Fclonefileat(srcFd, dstFd, "", 0)
}
