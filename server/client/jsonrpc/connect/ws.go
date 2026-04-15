package connect

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// 全局连接ID计数器
var connIDCounter uint64

// 获取下一个连接ID
func getNextConnID() uint64 {
	return atomic.AddUint64(&connIDCounter, 1)
}

// StartWs 从 WebSocket 启动 JSON-RPC，支持多会话
// addr: 监听地址，例如 "localhost:8080"
// path: WebSocket 路径，例如 "/jsonrpc"
func StartWs(addr, path string, handler func(string, func(string) error, uint64) (returnString string, exit bool), closeConn func(uint64)) error {
	// WebSocket 升级器
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// 允许所有来源
			return true
		},
	}

	// 存储所有活跃连接
	connsMutex := sync.Mutex{}
	conns := make(map[uint64]*websocket.Conn)

	// 处理 WebSocket 连接
	http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		// 升级连接到 WebSocket
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Printf("WebSocket 升级失败: %v\n", err)
			return
		}
		defer ws.Close()

		// 为当前连接分配 connID
		connID := getNextConnID()

		// 将连接添加到映射
		connsMutex.Lock()
		conns[connID] = ws
		connsMutex.Unlock()

		// 连接关闭时清理
		defer func() {
			connsMutex.Lock()
			delete(conns, connID)
			connsMutex.Unlock()
			closeConn(connID)
		}()

		// 处理来自 WebSocket 的消息
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				// 连接关闭或读取错误
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					fmt.Printf("WebSocket 错误: %v\n", err)
				}
				break
			}

			// 调用 handler 处理请求
			responseStr, shouldExit := handler(string(message), func(t string) error {
				// 发送消息到 WebSocket
				return ws.WriteMessage(websocket.TextMessage, []byte(t))
			}, connID)

			// 将响应写入 WebSocket
			if responseStr != "" {
				err := ws.WriteMessage(websocket.TextMessage, []byte(responseStr))
				if err != nil {
					fmt.Printf("WebSocket 写入错误: %v\n", err)
					break
				}
			}

			// 检查是否需要退出
			if shouldExit {
				break
			}
		}
	})

	// 启动 HTTP 服务器
	fmt.Printf("JSON-RPC WebSocket 服务启动在 ws://%s%s\n", addr, path)
	return http.ListenAndServe(addr, nil)
}

// StartWsWithHandler 便利函数，直接使用 Server 的 handle 和 closeConn 方法
// 这个函数可以在 jsonrpc.Server 中调用
func StartWsWithHandler(addr, path string, handler func(string, func(string) error, uint64) (returnString string, exit bool), closeConn func(uint64)) {
	if err := StartWs(addr, path, handler, closeConn); err != nil {
		fmt.Printf("WebSocket 服务启动失败: %v\n", err)
	}
}
