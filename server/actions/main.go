package actions

import (
	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
	u "github.com/cxykevin/alkaid0/utils"
)

// InitFuncs 初始化函数
func InitFuncs(srv *jsonrpc.Server) {
	jsonrpc.Set(srv, "initialize", Initialize)
	jsonrpc.Set(srv, "session/new", SessionNew)
	jsonrpc.Set(srv, "session/load", SessionLoad)
	jsonrpc.Set(srv, "session/list", SessionList)
	jsonrpc.Set(srv, "_close", Close)
}

// Close 关闭连接
func Close(req any, call func(string, any) error, connID uint64) (any, error) {
	for _, obj := range u.Default(bindedSessionOnConn, connID, []string{}) {
		closeSession(obj)
	}
	delete(bindedSessionOnConn, connID)
	return nil, nil
}
