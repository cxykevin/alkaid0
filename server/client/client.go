package client

import (
	"github.com/cxykevin/alkaid0/server/actions"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
)

// Start 启动
func Start() {
	srv := jsonrpc.New()
	actions.InitFuncs(srv)
	srv.Start()
}
