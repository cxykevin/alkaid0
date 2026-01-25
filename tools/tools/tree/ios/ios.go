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
		// 尝试 FICLONE
		err := cloneFile(int(s.Fd()), int(d.Fd()))
		if err != nil {
			// 不再尝试，不支持的文件系统
			// 交给 AI 扔到长期任务
			return err
		}
	} else {
		_, err = io.Copy(d, s)
		if err != nil {
			return err
		}
	}
	return nil
}
