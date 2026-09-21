package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

// maxMessageSize 单条 JSON-RPC 消息体上限（64MiB），防止异常/恶意 Content-Length 触发内存放大
const maxMessageSize = 64 << 20

// errFrame 帧协议/管道读取失败：连接已不可恢复，必须关闭传输层
var errFrame = errors.New("lsp frame error")

// NotificationHandler 处理服务器推送通知的回调
type NotificationHandler func(method string, params json.RawMessage)

// Transport JSON-RPC 2.0 传输层，基于 Content-Length 帧协议（LSP 标准）
type Transport struct {
	stdin  io.WriteCloser
	stdout *bufio.Reader

	pending   map[int64]chan<- *jsonrpcResponse
	pendingMu sync.Mutex
	nextID    atomic.Int64

	notifHandler NotificationHandler
	notifMu      sync.Mutex

	// writeMu 串行化整个帧（header+body）的写入，避免并发请求交错写坏 JSON-RPC 帧
	writeMu sync.Mutex

	closeOnce sync.Once
	closed    chan struct{}

	closeHandler   func()
	closeHandlerMu sync.Mutex
}

// NewTransport 创建传输层，启动后台读取 goroutine
func NewTransport(stdin io.WriteCloser, stdout io.ReadCloser) *Transport {
	t := &Transport{
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		pending: make(map[int64]chan<- *jsonrpcResponse),
		closed:  make(chan struct{}),
	}
	go t.readLoop()
	return t
}

// SendRequest 发送请求并等待响应
// ctx 用于控制超时，建议使用带超时的 context
func (t *Transport) SendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := t.nextID.Add(1)

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	respCh := make(chan *jsonrpcResponse, 1)

	t.pendingMu.Lock()
	t.pending[id] = respCh
	t.pendingMu.Unlock()

	// 发送请求
	if err := t.writeMessage(req); err != nil {
		t.pendingMu.Lock()
		delete(t.pending, id)
		t.pendingMu.Unlock()
		return nil, fmt.Errorf("send request %s: %w", method, err)
	}

	// 等待响应或超时
	select {
	case <-ctx.Done():
		t.pendingMu.Lock()
		delete(t.pending, id)
		t.pendingMu.Unlock()
		return nil, fmt.Errorf("request %s (id=%d): %w", method, id, ctx.Err())

	case resp, ok := <-respCh:
		if !ok {
			return nil, fmt.Errorf("request %s (id=%d): transport closed", method, id)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("request %s (id=%d): LSP error %d: %s", method, id, resp.Error.Code, resp.Error.Message)
		}
		if resp.Result == nil {
			return nil, nil
		}
		// 重新序列化 result 以确保返回 json.RawMessage
		b, err := json.Marshal(resp.Result)
		if err != nil {
			return nil, fmt.Errorf("marshal result: %w", err)
		}
		return json.RawMessage(b), nil
	}
}

// SendNotification 发送通知（不需要响应）
func (t *Transport) SendNotification(method string, params any) error {
	notif := jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return t.writeMessage(notif)
}

// SetNotificationHandler 设置服务器推送通知的处理回调
func (t *Transport) SetNotificationHandler(handler NotificationHandler) {
	t.notifMu.Lock()
	t.notifHandler = handler
	t.notifMu.Unlock()
}

// HasPending 返回是否存在尚未收到响应的在途请求（供空闲回收判断）
func (t *Transport) HasPending() bool {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()
	return len(t.pending) > 0
}

// Close 关闭传输层
func (t *Transport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.closed)

		t.pendingMu.Lock()
		for id, ch := range t.pending {
			close(ch)
			delete(t.pending, id)
		}
		t.pendingMu.Unlock()

		if e := t.stdin.Close(); e != nil {
			err = e
		}

		t.notifyCloseHandler()
	})
	return err
}

// Closed 返回传输层关闭信号；关闭后可用于探测连接已断开
func (t *Transport) Closed() <-chan struct{} {
	return t.closed
}

// SetCloseHandler 注册传输层关闭时的回调（仅在 Close 时触发，需在 Close 之前注册）
func (t *Transport) SetCloseHandler(handler func()) {
	t.closeHandlerMu.Lock()
	t.closeHandler = handler
	t.closeHandlerMu.Unlock()
}

// notifyCloseHandler 触发已注册的关闭回调
func (t *Transport) notifyCloseHandler() {
	t.closeHandlerMu.Lock()
	handler := t.closeHandler
	t.closeHandlerMu.Unlock()
	if handler != nil {
		handler()
	}
}

// readLoop 后台读取 stdout，将响应分发到对应的 pending channel
func (t *Transport) readLoop() {
	for {
		// 检查是否已关闭
		select {
		case <-t.closed:
			return
		default:
		}

		msg, err := t.readMessage()
		if err != nil {
			// 管道/帧已损坏（含 EOF、管道关闭、非法 Content-Length），无法原地恢复：
			// 必须关闭传输层并退出，否则会在已 EOF 的管道上空转占满 CPU
			if errors.Is(err, errFrame) {
				logger.Warn("lsp transport read failed, closing: %v", err)
				t.Close()
				return
			}
			// 仅单条消息内容非法（帧边界完整），跳过继续读
			logger.Warn("lsp transport message error: %v", err)
			continue
		}

		if msg == nil {
			continue // 通知消息或服务器主动请求，已在 readMessage 内处理
		}

		t.pendingMu.Lock()
		ch, ok := t.pending[msg.ID]
		if ok {
			delete(t.pending, msg.ID)
		}
		t.pendingMu.Unlock()

		if ok {
			ch <- msg
			close(ch)
		} else {
			logger.Warn("lsp transport: no pending request for id=%d", msg.ID)
		}
	}
}

// ---------------------------------------------------------------------------
// Content-Length 帧协议
// ---------------------------------------------------------------------------

// writeMessage 写入 JSON-RPC 消息（Content-Length 帧）
// header 与 body 必须作为整体串行写入，否则并发请求会交错破坏帧
func (t *Transport) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := t.stdin.Write([]byte(header)); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := t.stdin.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// readMessage 读取一条 JSON-RPC 消息
// 返回 nil 表示该消息不是对在途请求的响应（通知或服务器主动发起的请求，均已在内部处理）
func (t *Transport) readMessage() (*jsonrpcResponse, error) {
	contentLength, err := t.readContentLength()
	if err != nil {
		return nil, err
	}

	if contentLength <= 0 {
		return nil, nil
	}

	// 限制单条消息大小，防止异常 Content-Length 触发超大内存分配
	if contentLength > maxMessageSize {
		return nil, fmt.Errorf("%w: content-length %d exceeds limit %d", errFrame, contentLength, maxMessageSize)
	}

	// 读取 body
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(t.stdout, body); err != nil {
		return nil, fmt.Errorf("%w: read body: %w", errFrame, err)
	}

	// 先按原始字段判定消息类型：带 method 的是服务器发来的请求或通知
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("json unmarshal message: %w", err)
	}

	if methodRaw, hasMethod := raw["method"]; hasMethod {
		var method string
		_ = json.Unmarshal(methodRaw, &method)

		idRaw, hasID := raw["id"]
		if hasID && !isJSONNull(idRaw) {
			// 服务器 → 客户端请求：按 LSP 规范必须回复，绝不能投递给 pending（其 id 与本地请求无关）
			t.handleServerRequest(idRaw, method, raw["params"])
			return nil, nil
		}

		// 通知消息
		t.dispatchNotification(method, raw["params"])
		return nil, nil
	}

	idRaw, hasID := raw["id"]
	if !hasID || isJSONNull(idRaw) {
		// 既无 method 也无有效 id，非法消息，忽略
		return nil, nil
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("json unmarshal response: %w", err)
	}
	return &resp, nil
}

// isJSONNull 判断原始 JSON 值是否为 null
func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// dispatchNotification 将通知消息分发给已注册的回调
func (t *Transport) dispatchNotification(method string, params json.RawMessage) {
	t.notifMu.Lock()
	handler := t.notifHandler
	t.notifMu.Unlock()
	if handler == nil {
		return
	}
	if params == nil {
		params = json.RawMessage("null")
	}
	handler(method, params)
}

// handleServerRequest 处理服务器主动发起的请求
// 本项目未声明相关客户端能力，未知方法统一回复 null 结果，避免服务器一直等待。
// 回复放到独立 goroutine，避免 stdin 写阻塞时卡死读循环。
func (t *Transport) handleServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	logger.Warn("lsp transport: unhandled server request %s (id=%s), replying null result", method, string(id))
	go func() {
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      id, // json.RawMessage 原样输出，兼容数字或字符串 id
			"result":  nil,
		}
		if err := t.writeMessage(resp); err != nil {
			logger.Warn("lsp transport: reply server request %s (id=%s): %v", method, string(id), err)
		}
	}()
}

// readContentLength 读取 Content-Length 头
// 帧格式: Content-Length: N\r\n\r\n{body}
// 注意：找到 Content-Length 后必须读取其后的 \r\n 空行分隔符，
// 否则 io.ReadFull 会将空行作为 body 开头读取导致数据错位
func (t *Transport) readContentLength() (int, error) {
	var length int
	lengthSet := false
	for {
		line, err := t.stdout.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("%w: read header line: %w", errFrame, err)
		}
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// header 结束空行：Content-Length 已解析则返回；否则是 header 间的空白行，继续找
			if lengthSet {
				return length, nil
			}
			continue
		}

		// 按规范 Content-Length 可能在任意 header 顺序中出现，
		// 读到后不立即消费空行，而是继续循环直到真正的 header 结束空行。
		const prefix = "Content-Length: "
		if strings.HasPrefix(line, prefix) {
			if _, err := fmt.Sscanf(line, prefix+"%d", &length); err != nil {
				return 0, fmt.Errorf("%w: parse content-length: %w", errFrame, err)
			}
			if length <= 0 {
				return 0, fmt.Errorf("%w: invalid content-length: %d", errFrame, length)
			}
			lengthSet = true
		}
	}
}
