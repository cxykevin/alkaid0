package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

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

	closeOnce sync.Once
	closed    chan struct{}
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
	t.notifHandler = handler
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
	})
	return err
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
			// 正常关闭或连接断开
			if err == io.EOF || strings.Contains(err.Error(), "closed") {
				t.Close()
				return
			}
			logger.Warn("lsp transport read error: %v", err)
			continue
		}

		if msg == nil {
			continue // 通知消息，忽略
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
func (t *Transport) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}

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
// 返回 nil 表示通知消息（无需处理响应）
func (t *Transport) readMessage() (*jsonrpcResponse, error) {
	contentLength, err := t.readContentLength()
	if err != nil {
		return nil, err
	}

	if contentLength <= 0 {
		return nil, nil
	}

	// 读取 body
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(t.stdout, body); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// 尝试解析为响应消息
	var resp jsonrpcResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("json unmarshal response: %w", err)
	}

	// 检查是否有 ID（有 ID 才是响应，否则是通知）
	// 用 map 探测是否有 id 字段
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err == nil {
		if _, hasID := raw["id"]; !hasID {
			// 通知消息 — 分发到处理回调
			if t.notifHandler != nil {
				if method, ok := raw["method"].(string); ok {
					paramsRaw, _ := json.Marshal(raw["params"])
					t.notifHandler(method, paramsRaw)
				}
			}
			return nil, nil
		}
	}

	return &resp, nil
}

// readContentLength 读取 Content-Length 头
// 帧格式: Content-Length: N\r\n\r\n{body}
// 注意：找到 Content-Length 后必须读取其后的 \r\n 空行分隔符，
// 否则 io.ReadFull 会将空行作为 body 开头读取导致数据错位
func (t *Transport) readContentLength() (int, error) {
	for {
		line, err := t.stdout.ReadString('\n')
		if err != nil {
			return 0, fmt.Errorf("read header line: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// 空行可能是 header 之间的空白，跳过
			continue
		}

		const prefix = "Content-Length: "
		if strings.HasPrefix(line, prefix) {
			var length int
			if _, err := fmt.Sscanf(line, prefix+"%d", &length); err != nil {
				return 0, fmt.Errorf("parse content-length: %w", err)
			}
			if length <= 0 {
				return 0, fmt.Errorf("invalid content-length: %d", length)
			}
			// Content-Length 头行后必有 \r\n 空行标记 header 结束
			// 消耗这个空行，确保 readMessage 的 io.ReadFull 读到的是纯 body
			if _, err := t.stdout.ReadString('\n'); err != nil {
				return 0, fmt.Errorf("read blank line: %w", err)
			}
			return length, nil
		}
	}
}
