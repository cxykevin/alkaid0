package ios

import (
	"io"
	"os"
)

const maxCopySize = 1024 * 1024 * 256 // 256MB
// 超过 256M 使用 FICLONE

// Copy 拷贝文件
func Copy(origin, dist string) error {
	info, err := os.Stat(origin)
	if err != nil {
		return err
	}
	s, err := os.Open(origin)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.OpenFile(dist, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(dist)
		}
		d.Close()
	}()
	if info.Size() > maxCopySize {
		// 尝试 FICLONE（复用外层 err，使失败时 defer 的清理能生效）
		err = cloneFile(int(s.Fd()), int(d.Fd()))
		if err != nil {
			// FICLONE 不支持（macOS 参数语义差异或普通文件系统），回退按块复制，
			// 避免大文件克隆恒失败
			_, copyErr := io.Copy(d, s)
			if copyErr != nil {
				return copyErr
			}
		}
	} else {
		_, err = io.Copy(d, s)
		if err != nil {
			return err
		}
	}
	return nil
}
