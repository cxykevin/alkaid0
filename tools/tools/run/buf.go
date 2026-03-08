package run

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// AsyncPipeReader 异步管道读取器
type AsyncPipeReader struct {
	file     *os.File
	dataChan chan []byte
	errChan  chan error
	doneChan chan struct{}

	// 支持外部 context 和内部 context
	ctx         context.Context    // 外部传入的 context
	cancel      context.CancelFunc // 内部取消函数（组合外部 + 内部）
	internalCtx context.Context    // 内部 context，用于 Close

	wg     sync.WaitGroup
	closed int32 // 原子操作标记关闭状态

	// 统计
	bytesRead int64
	readCount int64
}

// NewAsyncPipeReader 创建异步管道读取器
// file: 管道文件（必须是 *os.File 类型以支持 SetReadDeadline）
// bufferSize: dataChan 缓冲大小
// ctx: 外部 context，可选（传 nil 则使用 background）
func NewAsyncPipeReader(ctx context.Context, file *os.File, bufferSize int) *AsyncPipeReader {
	if ctx == nil {
		ctx = context.Background()
	}

	// 创建内部 context，用于 Close 控制
	internalCtx, internalCancel := context.WithCancel(context.Background())

	// 组合外部 context 和内部 context
	// 任一取消都会触发
	combinedCtx, combinedCancel := context.WithCancel(ctx)

	go func() {
		select {
		case <-ctx.Done():
			combinedCancel()
		case <-internalCtx.Done():
			combinedCancel()
		}
	}()

	r := &AsyncPipeReader{
		file:        file,
		dataChan:    make(chan []byte, bufferSize),
		errChan:     make(chan error, 1),
		doneChan:    make(chan struct{}),
		ctx:         combinedCtx,
		cancel:      combinedCancel,
		internalCtx: internalCtx,
	}

	r.wg.Add(1)
	go r.readLoop(internalCancel)

	return r
}

// readLoop 后台读取循环
func (r *AsyncPipeReader) readLoop(internalCancel context.CancelFunc) {
	defer r.wg.Done()
	defer close(r.dataChan)
	defer close(r.doneChan)
	defer internalCancel() // 确保内部 context 被取消

	buffer := make([]byte, 64*1024)

	for {
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		// 设置可中断的读取超时
		if err := r.file.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			r.sendError(fmt.Errorf("设置超时失败: %w", err))
			return
		}

		n, err := r.file.Read(buffer)

		if n > 0 {
			atomic.AddInt64(&r.bytesRead, int64(n))
			atomic.AddInt64(&r.readCount, 1)

			// 拷贝数据
			data := make([]byte, n)
			copy(data, buffer[:n])

			select {
			case r.dataChan <- data:
			case <-r.ctx.Done():
				return
			}
		}

		if err != nil {
			// 超时错误，继续循环
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}

			// EOF 正常结束
			if err == io.EOF {
				return
			}

			r.sendError(err)
			return
		}
	}
}

// sendError 安全地发送错误
func (r *AsyncPipeReader) sendError(err error) {
	select {
	case r.errChan <- err:
	case <-r.ctx.Done():
	}
}

// TryRead 非阻塞尝试读取
func (r *AsyncPipeReader) TryRead() ([]byte, bool, error) {
	select {
	case data, ok := <-r.dataChan:
		if !ok {
			return nil, false, io.EOF
		}
		return data, true, nil

	case err := <-r.errChan:
		return nil, false, err

	default:
		return nil, false, nil
	}
}

// Read 实现 io.Reader 接口（阻塞，但可被 context 取消）
func (r *AsyncPipeReader) Read(p []byte) (int, error) {
	// 先尝试非阻塞
	data, ok, err := r.TryRead()
	if err != nil {
		return 0, err
	}
	if ok {
		n := copy(p, data)
		return n, nil
	}

	// 阻塞等待，但可被 context 取消
	select {
	case data, ok := <-r.dataChan:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, data)
		return n, nil

	case err := <-r.errChan:
		return 0, err

	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	}
}

// CopyTo 复制到写入函数，支持 context 取消和空闲超时
func (r *AsyncPipeReader) CopyTo(ctx context.Context, writeFunc func([]byte) error) error {
	// 合并外部 context 和 reader 的 context
	mergedCtx, cancel := r.mergeContext(ctx)
	defer cancel()

	for {
		select {
		case data, ok := <-r.dataChan:
			if !ok {
				return nil // 正常完成
			}

			if err := writeFunc(data); err != nil {
				return fmt.Errorf("写入失败: %w", err)
			}

		case err := <-r.errChan:
			return err

		case <-mergedCtx.Done():
			return mergedCtx.Err()
		}
	}
}

// CopyToNonBlocking 完全非阻塞复制
func (r *AsyncPipeReader) CopyToNonBlocking(writeFunc func([]byte) error) (int64, error) {
	var total int64

	for {
		data, ok, err := r.TryRead()
		if err != nil {
			return total, err
		}
		if !ok {
			return total, nil
		}

		if err := writeFunc(data); err != nil {
			return total, err
		}
		total += int64(len(data))
	}
}

// WaitContext 等待完成或 context 取消
func (r *AsyncPipeReader) WaitContext(ctx context.Context) error {
	select {
	case <-r.doneChan:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Wait 等待读取完成（永久阻塞）
func (r *AsyncPipeReader) Wait() {
	<-r.doneChan
}

// Close 关闭读取器
func (r *AsyncPipeReader) Close() error {
	if !atomic.CompareAndSwapInt32(&r.closed, 0, 1) {
		return nil // 已关闭
	}

	// 触发内部 context 取消
	r.internalCtx.Done()
	r.cancel()

	// 关闭文件（这会中断正在进行的 Read）
	r.file.Close()

	// 等待 Goroutine 退出
	r.wg.Wait()

	// 清空剩余数据
	for range r.dataChan {
	}

	return nil
}

// Context 返回当前 context
func (r *AsyncPipeReader) Context() context.Context {
	return r.ctx
}

// Done 返回完成 channel
func (r *AsyncPipeReader) Done() <-chan struct{} {
	return r.doneChan
}

// Err 返回错误
func (r *AsyncPipeReader) Err() error {
	select {
	case err := <-r.errChan:
		return err
	default:
		if err := r.ctx.Err(); err != nil {
			return err
		}
		return nil
	}
}

// Stats 获取统计信息
func (r *AsyncPipeReader) Stats() (bytesRead, readCount int64, queued int, done bool) {
	bytesRead = atomic.LoadInt64(&r.bytesRead)
	readCount = atomic.LoadInt64(&r.readCount)
	queued = len(r.dataChan)

	select {
	case <-r.doneChan:
		done = true
	default:
	}

	return
}

// mergeContext 合并两个 context（任一取消都触发）
func (r *AsyncPipeReader) mergeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return r.ctx, func() {}
	}

	// 如果传入的就是 r.ctx，直接返回
	if ctx == r.ctx {
		return ctx, func() {}
	}

	merged, cancel := context.WithCancel(ctx)

	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-r.ctx.Done():
			cancel()
		}
	}()

	return merged, cancel
}
