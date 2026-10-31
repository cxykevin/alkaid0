package ios

import (
	"io"
	"os"
)

const maxCopySize = 1024 * 1024 * 256 // 256MB
// 超过 256M 使用 FICLONE

// copyBufferSize 分块复制的块大小：1 MiB 在吞吐与进度回调频率之间折中。
const copyBufferSize = 1024 * 1024

// maxCopyLimit、cloneFileFn 与 removeFileFn 是拷贝阈值、克隆实现与失败清理的
// 间接层，仅为可测试性暴露（见 ios_test.go 的 FICLONE 回退路径用例）；生产代码不应修改它们。
var (
	maxCopyLimit int64 = maxCopySize
	cloneFileFn        = cloneFile
	// removeFileFn 清理复制失败时留下的半成品目标文件。
	// 它接收已经关闭的目标 *os.File，仅用于让测试断言"关闭先于删除"这一顺序约束。
	removeFileFn = func(_ *os.File, dist string) error { return os.Remove(dist) }
)

// Copy 拷贝文件
func Copy(origin, dist string) error {
	return CopyWithProgress(origin, dist, nil)
}

// CopyWithProgress 拷贝文件，并在分块复制时回调进度（copied/total 字节）。
//
// 大文件（> maxCopyLimit）先尝试 CoW 克隆（FICLONE/CloneFile/DuplicateExtents，
// 零拷贝且瞬间完成）；文件系统不支持时回退到分块复制，此时通过 onProgress
// 汇报进度。onProgress 可为 nil；CoW 成功时只回调一次 (total, total)。
func CopyWithProgress(origin, dist string, onProgress func(copied, total int64)) error {
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
	// copyErr 是"复制是否失败"的唯一判据，并与下面的清理绑定。
	// 不能复用 cloneFile 的返回值：FICLONE 不受支持而回退分块复制成功时，
	// 那个错误仍然非 nil，会把刚复制好的目标文件删掉、却向上返回成功。
	var copyErr error

	if info.Size() > maxCopyLimit {
		// 大文件优先尝试 FICLONE（零拷贝）；不支持时回退按块复制
		if cloneErr := cloneFileFn(int(s.Fd()), int(d.Fd())); cloneErr != nil {
			// 回退前把目标归零，避免克隆失败时残留在目标里的半截数据与回退内容叠加
			if _, seekErr := d.Seek(0, io.SeekStart); seekErr != nil {
				copyErr = seekErr
			} else {
				_ = d.Truncate(0)
				copyErr = copyFileData(d, s, info.Size(), onProgress)
			}
		} else if onProgress != nil {
			onProgress(info.Size(), info.Size())
		}
	} else {
		copyErr = copyFileData(d, s, info.Size(), onProgress)
	}

	// 必须先关闭目标句柄再清理：Windows 上删除仍处于打开状态的文件会因共享冲突
	// 失败，半成品目标文件会残留（原 defer 里 remove 在 Close 之前，测试
	// TestCopyRemovesDestinationOnRealFailure 在 windows-latest 上暴露了这一点）。
	// 关闭失败意味着数据可能没有落盘，同样按复制失败处理，不留下可疑的目标文件。
	if closeErr := d.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = removeFileFn(d, dist)
	}
	return copyErr
}

// copyFileData 按固定块大小复制，并在每块写入后回调进度。
// 返回的错误与 io.Copy 语义一致；EOF 不算错误。
func copyFileData(dst io.Writer, src io.Reader, total int64, onProgress func(copied, total int64)) error {
	buf := make([]byte, copyBufferSize)
	var copied int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			copied += int64(n)
			if onProgress != nil {
				onProgress(copied, total)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
