// Package client 实现服务端客户端管理与 WebSocket 服务启动
package client

import (
	"github.com/cxykevin/alkaid0/server/actions"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
)

// var logger = log.New("client")

// Start 启动
func Start() {
	srv := jsonrpc.New()
	actions.InitFuncs(srv)

	srv.StartWs()
	srv.Start()
}
