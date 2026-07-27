package codebase

import (
	"context"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// EmbedTask 单个文件的嵌入任务
type EmbedTask struct {
	// EmbedText 用于嵌入计算的文本内容
	EmbedText string
	// FullContent 查询时需要返回的完整内容
	FullContent string
	// FilePath 源文件路径
	FilePath string
	// Symbol 文件内符号标识，""=整个文件
	Symbol string
	// Tags 标签列表
	Tags []string
	// Priority true=插队到队头，false=加入队尾
	Priority bool
	// Done 可选通道，任务完成时关闭
	Done chan<- struct{}
}

// embedHash 计算嵌入文本的 SHA256 摘要
func embedHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// tagsToJSON 将标签列表序列化为 JSON 数组字符串
func tagsToJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b := make([]byte, 0, len(tags)*16+2)
	b = append(b, '[')
	for i, t := range tags {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		b = append(b, []byte(escapeJSONString(t))...)
		b = append(b, '"')
	}
	b = append(b, ']')
	return string(b)
}

func escapeJSONString(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\\':
			result = append(result, '\\', c)
		case '\n':
			result = append(result, '\\', 'n')
		case '\r':
			result = append(result, '\\', 'r')
		case '\t':
			result = append(result, '\\', 't')
		default:
			if c < 0x20 {
				result = append(result, '\\', 'u', '0', '0', hexChar(c>>4), hexChar(c&0xf))
			} else {
				result = append(result, c)
			}
		}
	}
	return string(result)
}

func hexChar(v byte) byte {
	if v < 10 {
		return '0' + v
	}
	return 'a' + v - 10
}

// QueueManager 单个目录的任务队列
type QueueManager struct {
	mu     sync.Mutex
	queue  list.List // 元素类型 *EmbedTask
	notify chan struct{}
	paused bool
	closed bool
}

// NewQueueManager 创建新的队列管理器
func NewQueueManager() *QueueManager {
	return &QueueManager{
		notify: make(chan struct{}, 1),
	}
}

// Push 将任务加入队列。Priority=true 时插到队首，否则加入队尾
func (qm *QueueManager) Push(task *EmbedTask) {
	qm.mu.Lock()
	if qm.closed {
		qm.mu.Unlock()
		return
	}

	if task.Priority {
		qm.queue.PushFront(task)
	} else {
		qm.queue.PushBack(task)
	}
	qm.mu.Unlock()

	// 非阻塞通知
	select {
	case qm.notify <- struct{}{}:
	default:
	}
}

// WaitPop 阻塞等待直到有可用任务。ctx 取消时返回 nil
func (qm *QueueManager) WaitPop(ctx context.Context) *EmbedTask {
	for {
		qm.mu.Lock()
		if qm.queue.Len() > 0 && !qm.paused && !qm.closed {
			elem := qm.queue.Front()
			qm.queue.Remove(elem)
			qm.mu.Unlock()
			return elem.Value.(*EmbedTask)
		}
		if qm.closed {
			qm.mu.Unlock()
			return nil
		}
		qm.mu.Unlock()

		select {
		case <-qm.notify:
		case <-ctx.Done():
			return nil
		}
	}
}

// Len 返回当前队列长度
func (qm *QueueManager) Len() int {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	return qm.queue.Len()
}

// Pause 暂停队列处理（WaitPop 不会返回任务）
func (qm *QueueManager) Pause() {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.paused = true
}

// Resume 恢复队列处理
func (qm *QueueManager) Resume() {
	qm.mu.Lock()
	qm.paused = false
	qm.mu.Unlock()

	select {
	case qm.notify <- struct{}{}:
	default:
	}
}

// Clear 清空队列
func (qm *QueueManager) Clear() {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.queue.Init()
}

// Close 关闭队列，所有阻塞的 WaitPop 返回 nil
func (qm *QueueManager) Close() {
	qm.mu.Lock()
	qm.closed = true
	qm.mu.Unlock()

	select {
	case qm.notify <- struct{}{}:
	default:
	}
}
