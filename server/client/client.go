// Package client 实现服务端客户端管理与 WebSocket 服务启动
package client

import (
	"github.com/cxykevin/alkaid0/server/actions"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
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

	srv.StartWs()
	srv.Start()

	// 当 stdio 服务被禁用或 stdin 在服务环境下关闭后，
	// 进程没有阻塞点会立即退出。用 select{} 永久阻塞，
	// 由 startup.go 的信号处理 goroutine 在收到 SIGTERM/SIGINT 时
	// 调用 os.Exit(0) 来退出。
	select {}
}
