package connect

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/library/chancall"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/product"
	"github.com/cxykevin/alkaid0/server/openai"
	"github.com/cxykevin/alkaid0/stats"
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

// var (
// 	activeConns   = make(map[uint64]*websocket.Conn)
// 	activeConnsMu sync.Mutex
// )

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

// serverKeyParams 从查询参数中提取服务端 key 时尝试的参数名（保持与旧客户端兼容）。
var serverKeyParams = []string{"token", "Token", "TOKEN", "authorization", "Authorization", "auth", "Auth", "AUTH", "session", "Session", "passwd", "Passwd", "password", "Password", "access_token", "AccessToken", "key", "Key", "KEY", "k", "s", "p"}

// httpRequestServerKey 从请求的查询参数或 Authorization 头提取服务端 key。
// 供 WebSocket 升级与 /info 等 HTTP 端点共用同一套鉴权。
func httpRequestServerKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	vals := r.URL.Query()
	for _, name := range serverKeyParams {
		if token := vals.Get(name); token != "" {
			return token
		}
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return ""
	}
	// 支持 "Bearer <key>"（大小写不敏感）与裸 key 两种形式
	if len(auth) > len("bearer ") && strings.EqualFold(auth[:len("bearer ")], "bearer ") {
		return strings.TrimSpace(auth[len("bearer "):])
	}
	return auth
}

// getSessionCount 供 /info 返回会话/DB 数；包级变量作为测试缝，
// 避免测试依赖 chancall 消费者导致阻塞。
var getSessionCount = GetSessionCount

// handleInfo 处理 /info：返回版本、进程内存与会话统计。
// 这些数据包含运行态信息，必须与 WebSocket 升级同级鉴权（此前无鉴权即可读取）。
func handleInfo(w http.ResponseWriter, r *http.Request) {
	// 恒定时间比较，避免时序侧信道泄露 key 长度/内容
	if subtle.ConstantTimeCompare([]byte(httpRequestServerKey(r)), []byte(config.GlobalConfig.Server.Key)) != 1 {
		loggerWs.Error("invalid token on /info, rejecting request")
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Server", product.UserAgent)
	var state runtime.MemStats
	runtime.ReadMemStats(&state)
	ss, db := getSessionCount()
	_ = json.NewEncoder(w).Encode(u.H{
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
		"usage": stats.Snapshot(),
	})
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

	// 显式 mux 允许在同一 HTTP server 上挂载 ACP 与 OpenAI API。
	mux := http.NewServeMux()

	// 根路径返回简单 JSON
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

	// /info：运行态信息，必须携带与 WebSocket 相同的 key（见 handleInfo）
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			return
		}
		handleInfo(w, r)
	})

	// 处理 WebSocket 连接
	mux.HandleFunc(config.GlobalConfig.Server.Path, func(w http.ResponseWriter, r *http.Request) {
		// 检查token（查询参数或 Authorization 头）
		token := httpRequestServerKey(r)
		// 恒定时间比较，避免时序侧信道泄露 key 长度/内容
		if subtle.ConstantTimeCompare([]byte(token), []byte(config.GlobalConfig.Server.Key)) != 1 {
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

		// 创建 context 用于优雅退出 ping goroutine
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 设置 pong handler，每次收到 pong 时重置读超时
		ws.SetPongHandler(func(string) error {
			ws.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})
		// 设置初始读超时
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))

		// 启动 ping goroutine，定期发送 ping 保持连接活跃
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					writeMu.Lock()
					ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
					err := ws.WriteMessage(websocket.PingMessage, nil)
					writeMu.Unlock()
					if err != nil {
						loggerWs.Debug("ping failed: %v", err)
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()

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
					ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
					err := ws.WriteMessage(websocket.TextMessage, []byte(t))
					writeMu.Unlock()
					return err
				}, connID)

				// 将响应写入 WebSocket
				if responseStr != "" {
					writeMu.Lock()
					ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
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

	// 注册 OpenAI-compatible proxy，共用当前 WebSocket listener。
	openai.NewHandler().Register(mux)

	// 启动 HTTP 服务器（支持优雅关闭）
	wsHTTPServer = &http.Server{Addr: addr, Handler: mux}
	loggerWs.Info("webSocket service started in ws://%s%s", addr, path)
	fmt.Fprintf(os.Stderr, "WebSocket service started in ws://%s%s\n", addr, path)
	go func() {
		if err := wsHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			loggerWs.Error("ws server error: %v", err)
		}
	}()
	return nil
}
