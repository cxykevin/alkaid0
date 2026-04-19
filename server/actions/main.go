package actions

import (
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
	u "github.com/cxykevin/alkaid0/utils"
)

var logger = log.New("actions")

// InitFuncs 初始化函数
func InitFuncs(srv *jsonrpc.Server) {
	logger.Info("init functions")
	jsonrpc.Set(srv, "initialize", Initialize)
	jsonrpc.Set(srv, "session/new", SessionNew)
	jsonrpc.Set(srv, "session/load", SessionLoad)
	jsonrpc.Set(srv, "session/list", SessionList)
	jsonrpc.Set(srv, "session/set_config_option", SessionSetConfigOption)
	jsonrpc.Set(srv, "session/set_model", SessionSetModel)
	jsonrpc.Set(srv, "session/setModel", SessionSetModel)
	jsonrpc.Set(srv, "unstable_setSessionModel", SessionSetModel)
	jsonrpc.Set(srv, "session/prompt", SessionPrompt)
	jsonrpc.Set(srv, "session/cancel", SessionCancel)
	jsonrpc.Set(srv, "_close", Close)
}

// Close 关闭连接
func Close(req any, call func(string, any) error, connID uint64) (any, error) {
	for _, sessionID := range u.Default(bindedSessionOnConn, connID, []string{}) {
		closeSession(sessionID)
		// 注销该连接与会话的绑定
		unregisterConnCall(connID, sessionID)
	}
	delete(bindedSessionOnConn, connID)
	delete(clientConnCaps, connID)
	return nil, nil
}
