package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/library/chancall"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/product"
	u "github.com/cxykevin/alkaid0/utils"
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

// wsHTTPServer 全局 HTTP 服务器引用，用于优雅关闭
var wsHTTPServer *http.Server

// ShutdownWs 优雅关闭 WebSocket HTTP 服务器
func ShutdownWs(ctx context.Context) error {
	if wsHTTPServer == nil {
		return nil
	}
	return wsHTTPServer.Shutdown(ctx)
}

var (
	activeConns   = make(map[uint64]*websocket.Conn)
	activeConnsMu sync.Mutex
)

// GetSessionCount 返回当前活跃 Session,DB 数
func GetSessionCount() (int, int) {
	ev := chancall.EventChan{
		Consumer: "actions/states",
		In:       nil,
		Out:      make(chan chancall.Ret, 1),
	}
	ev.In = nil
	chancall.ActChan <- ev
	ret := <-ev.Out
	if ret.Err != nil {
		return -1, -1
	}
	v := ret.Ret
	ret2, ok := v.(u.H)
	if !ok {
		return -1, -1
	}
	return u.AnyDefault(u.Default(ret2, "sessions", -1), -1), u.AnyDefault(u.Default(ret2, "dbs", -1), -1)
}

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

	// 根路径返回简单 JSON
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Server", product.UserAgent)
		json.NewEncoder(w).Encode(map[string]string{
			"server":  "alkaid0",
			"version": product.Version,
		})
	})

	// 根路径返回简单 JSON
	http.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Server", product.UserAgent)
		var state runtime.MemStats
		runtime.ReadMemStats(&state)
		ss, db := GetSessionCount()
		json.NewEncoder(w).Encode(u.H{
			"version": product.Version,
			"system": u.H{
				"pid":        os.Getpid(),
				"goroutines": runtime.NumGoroutine(),
				"mem": u.H{
					"alloc": state.Alloc,
					"sys":   state.Sys,
				},
				"gc": u.H{
					"num":   state.NumGC,
					"pause": float32(time.Duration(state.PauseTotalNs) / time.Second),
					"cpu":   state.GCCPUFraction,
				},
			},
			"network": u.H{
				"sessions": ss,
				"dbs":      db,
			},
		})
	})

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
			loggerWs.Error("invalid token, rejecting connection")
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
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
		var writeMu sync.Mutex

		// 每个连接最多 32 个并发消息处理 goroutine
		sem := make(chan struct{}, 32)

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

			// 获取 semaphore slot（backpressure：队列满时阻塞读取）
			sem <- struct{}{}

			// 在独立 goroutine 中处理每个请求，防止长时间阻塞（如 session/prompt 等待 AI 响应）
			// 使得 session/cancel 等请求能在此连接上被并发处理
			go func(msg []byte) {
				defer func() { <-sem }()
				responseStr, shouldExit := handler(string(msg), func(t string) error {
					writeMu.Lock()
					err := ws.WriteMessage(websocket.TextMessage, []byte(t))
					writeMu.Unlock()
					return err
				}, connID)

				// 将响应写入 WebSocket
				if responseStr != "" {
					writeMu.Lock()
					err := ws.WriteMessage(websocket.TextMessage, []byte(responseStr))
					writeMu.Unlock()
					if err != nil {
						loggerWs.Error("websocket write error: %v", err)
					}
				}

				// 检查是否需要退出
				if shouldExit {
					writeMu.Lock()
					ws.Close()
					writeMu.Unlock()
				}
			}(message)
		}

		loggerWs.Info("connection close: %d", connID)
	})

	// 启动 HTTP 服务器（支持优雅关闭）
	wsHTTPServer = &http.Server{Addr: addr}
	loggerWs.Info("webSocket service started in ws://%s%s", addr, path)
	fmt.Fprintf(os.Stderr, "WebSocket service started in ws://%s%s\n", addr, path)
	go func() {
		if err := wsHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			loggerWs.Error("ws server error: %v", err)
		}
	}()
	return nil
}
