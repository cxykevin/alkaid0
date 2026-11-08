// Package client 实现服务端客户端管理与 WebSocket 服务启动
package client

import (
	"github.com/cxykevin/alkaid0/server/actions"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc/connect"
)

// var logger = log.New("client")

// Start 启动服务
func Start() {
	srv := jsonrpc.New()
	actions.InitFuncs(srv)
	// cron 调度器：打开配置目录下的 cron.sqlite 并常驻运行。
	// 必须在 InitFuncs 之后启动（rpcSrv 已就绪），且放在 StartWs 之前，
	// 保证服务开始接受连接时 cron 状态已经加载完成。
	actions.StartCron()
	// /gc HTTP 端点（与 SIGUSR2 共用 actions.ForceGC）：connect 不能导入
	// server/actions（会形成循环依赖），因此在这里注入实现。
	connect.SetForceGC(func() any { return actions.ForceGC() })
	// 强制 GC 回收空闲会话时，直接断开这些会话的客户端连接，
	// 由客户端重连后 session/resume 恢复。
	actions.SetConnectionCloser(connect.CloseConnections)

	srv.StartWs()
	srv.Start()

	// 当 stdio 服务被禁用或 stdin 在服务环境下关闭后，
	// 进程没有阻塞点会立即退出。用 select{} 永久阻塞，
	// 由 startup.go 的信号处理 goroutine 在收到 SIGTERM/SIGINT 时
	// 调用 os.Exit(0) 来退出。
	select {}
}
