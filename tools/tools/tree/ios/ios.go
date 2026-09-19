package ios

import (
	"io"
	"os"
)

const maxCopySize = 1024 * 1024 * 256 // 256MB
// 超过 256M 使用 FICLONE

// maxCopyLimit 与 cloneFileFn 是拷贝阈值与克隆实现的间接层，
// 仅为可测试性暴露（见 ios_test.go 的 FICLONE 回退路径用例）；生产代码不应修改它们。
var (
	maxCopyLimit int64 = maxCopySize
	cloneFileFn        = cloneFile
)

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
	// copyErr 是"复制是否失败"的唯一判据，并与 defer 里的清理绑定。
	// 不能复用 cloneFile 的返回值：FICLONE 不受支持而回退 io.Copy 成功时，
	// 那个错误仍然非 nil，会把刚复制好的目标文件删掉、却向上返回成功。
	var copyErr error
	defer func() {
		if copyErr != nil {
			os.Remove(dist)
		}
		d.Close()
	}()

	if info.Size() > maxCopyLimit {
		// 大文件优先尝试 FICLONE（零拷贝）；不支持时回退按块复制
		if err := cloneFileFn(int(s.Fd()), int(d.Fd())); err != nil {
			// 回退前把目标归零，避免克隆失败时残留在目标里的半截数据与回退内容叠加
			if _, seekErr := d.Seek(0, io.SeekStart); seekErr != nil {
				copyErr = seekErr
			} else {
				_ = d.Truncate(0)
				_, copyErr = io.Copy(d, s)
			}
		}
	} else {
		_, copyErr = io.Copy(d, s)
	}
	return copyErr
}
