package connect

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
	"github.com/gorilla/websocket"
)

// 全局连接ID计数器
var connIDCounter uint64 = 17

// 获取下一个连接ID
func getNextConnID() uint64 {
	return atomic.AddUint64(&connIDCounter, 1)
}

var loggerWs = log.New("connect(ws)")

// readLimit 限制 WebSocket 消息的大小
const readLimit = 16 * 1024 * 1024

// StartWs 从 WebSocket 启动 JSON-RPC，支持多会话
// addr: 监听地址，例如 "localhost:8080"
// path: WebSocket 路径，例如 "/jsonrpc"
func StartWs(handler func(string, func(string) error, uint64) (returnString string, exit bool), closeConn func(uint64)) error {
	if config.GlobalConfig.Server.Key == "" {
		fmt.Fprintf(os.Stderr, "WebSocket service couldn't start, because the key is empty. Please set the key in the configuration file.\n")
		loggerWs.Error("ws server start failed beacuse key is empty")
		return nil
	}
	addr := fmt.Sprintf("%s:%d", config.GlobalConfig.Server.Host, config.GlobalConfig.Server.Port)
	path := config.GlobalConfig.Server.Path

	// 存储所有活跃连接
	connsMutex := sync.Mutex{}
	conns := make(map[uint64]*websocket.Conn)

	// 处理 WebSocket 连接
	http.HandleFunc(config.GlobalConfig.Server.Path, func(w http.ResponseWriter, r *http.Request) {
		vals := r.URL.Query()
		if len(vals) == 0 {
			loggerWs.Error("no query params")
			return
		}
		// 检查token
		token := ""
		for _, val := range []string{"token", "Token", "TOKEN", "authorization", "Authorization", "auth", "Auth", "AUTH", "session", "Session", "passwd", "Passwd", "password", "Password", "access_token", "AccessToken", "key", "Key", "KEY", "k", "s", "p"} {
			if token = vals.Get(val); token != "" {
				break
			}
		}
		if token != config.GlobalConfig.Server.Key {
			loggerWs.Error("invalid token")
		}
		// 升级连接到 WebSocket
		upgder := websocket.Upgrader{
			ReadBufferSize:  readLimit,
			WriteBufferSize: readLimit,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		}
		ws, err := upgder.Upgrade(w, r, nil)
		if err != nil {
			loggerWs.Error("websocket upgrade failed: %v", err)
			return
		}
		defer ws.Close()

		ws.SetReadLimit(readLimit)

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

		loggerWs.Info("new connection: %d", connID)

		// 处理来自 WebSocket 的消息
		for {
			_, message, err := ws.ReadMessage()
			if err != nil {
				// 连接关闭或读取错误
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					loggerWs.Error("websocket error: %v", err)
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
					loggerWs.Error("websocket write error: %v", err)
					break
				}
			}

			// 检查是否需要退出
			if shouldExit {
				break
			}
		}

		loggerWs.Info("connection close: %d", connID)
	})

	// 启动 HTTP 服务器
	loggerWs.Info("webSocket service started in ws://%s%s", addr, path)
	fmt.Fprintf(os.Stderr, "WebSocket service started in ws://%s%s\n", addr, path)
	return http.ListenAndServe(addr, nil)
}
