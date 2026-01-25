//go:build linux || darwin

package ios

import "golang.org/x/sys/unix"

func cloneFile(srcFd, dstFd int) error {
	return unix.IoctlFileClone(dstFd, srcFd)
}
