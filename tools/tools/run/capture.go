package run

import (
	"fmt"
	"sync"
)

// 后台命令输出捕获上限。
//
// 为什么需要：任务输出此前写进一个无上限的 bytes.Buffer，一条 `yes`、`cat 大文件`
// 或死循环打印日志的命令就能把服务端内存吃光（而任务本身是"后台"的，可能长期运行）。
// 这里对捕获量做硬上限：保留开头一段与最近一段，中间丢弃并显式标注。
//
// 之所以头尾都留：
//   - 头部通常是命令回显与"第一个错误"，排查时最有用；
//   - 尾部是运行期内容快照（job.content / @temp/<run> / 前端推送）的数据来源，
//     只留头部会让长任务的实时视图停止更新。
const (
	maxCaptureHead = 512 << 10  // 512 KiB：保留开头
	maxCaptureTail = 1536 << 10 // 1.5 MiB：保留最近
)

// cappedBuffer 是带总量上限的输出缓冲，提供与 bytes.Buffer 同形的
// Write / WriteString / Bytes / String 方法。
//
// 并发：内部自带互斥，可安全地被命令输出写入方与内容刷新协程同时使用。
type cappedBuffer struct {
	mu      sync.Mutex
	head    []byte
	tail    []byte
	dropped int64
}

// Write 追加输出，超出上限的部分被丢弃并计入 dropped。
func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.head) < maxCaptureHead {
		room := maxCaptureHead - len(c.head)
		if room > len(p) {
			room = len(p)
		}
		c.head = append(c.head, p[:room]...)
		p = p[room:]
	}

	if len(p) > 0 {
		c.tail = append(c.tail, p...)
		if excess := len(c.tail) - maxCaptureTail; excess > 0 {
			c.tail = c.tail[excess:]
			c.dropped += int64(excess)
		}
		// 切片前移不会释放底层数组；定期搬到新数组，避免 append 无限增长。
		if cap(c.tail) > 2*maxCaptureTail {
			fresh := make([]byte, len(c.tail))
			copy(fresh, c.tail)
			c.tail = fresh
		}
	}

	return n, nil
}

// WriteString 追加字符串输出。
func (c *cappedBuffer) WriteString(s string) (int, error) {
	return c.Write([]byte(s))
}

// Bytes 返回"头部 + 截断标注 + 尾部"的完整内容。
func (c *cappedBuffer) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dropped == 0 {
		out := make([]byte, 0, len(c.head)+len(c.tail))
		out = append(out, c.head...)
		out = append(out, c.tail...)
		return out
	}
	marker := fmt.Sprintf("\n...[alkaid0] 输出超过上限，已省略中间 %d 字节]...\n", c.dropped)
	out := make([]byte, 0, len(c.head)+len(marker)+len(c.tail))
	out = append(out, c.head...)
	out = append(out, marker...)
	out = append(out, c.tail...)
	return out
}

// String 返回捕获到的内容（含截断标注）。
func (c *cappedBuffer) String() string { return string(c.Bytes()) }

// Dropped 返回被丢弃的字节数（0 表示未截断）。
func (c *cappedBuffer) Dropped() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}
