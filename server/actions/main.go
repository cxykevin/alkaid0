package actions

import (
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
)

var logger = log.New("actions")

// rpcSrv jsonrpc 服务器实例（供 request_permission 等出站请求的响应关联使用）
var rpcSrv *jsonrpc.Server

// InitFuncs 初始化函数
func InitFuncs(srv *jsonrpc.Server) {
	rpcSrv = srv
	logger.Info("init functions")

	{ // 系统
		jsonrpc.Set(srv, "initialize", Initialize)
		jsonrpc.Set(srv, "_close", Close)

		jsonrpc.Set(srv, "alk.cxykevin.top/config/get", ConfigGet)
		jsonrpc.Set(srv, "alk.cxykevin.top/config/set", ConfigSet)
		jsonrpc.Set(srv, "alk.cxykevin.top/config/reload", reloadFunc)
		jsonrpc.Set(srv, "alk.cxykevin.top/reload_config", reloadFunc)
	}

	{ // 会话
		jsonrpc.Set(srv, "session/new", SessionNew)
		jsonrpc.Set(srv, "session/resume", SessionResume)
		jsonrpc.Set(srv, "session/close", SessionClose)
		jsonrpc.Set(srv, "session/list", SessionList)
		jsonrpc.Set(srv, "session/update", HandleSessionUpdate)
		jsonrpc.Set(srv, "session/delete", SessionDelete)
		jsonrpc.Set(srv, "session/set_config_option", SessionSetConfigOption)
		jsonrpc.Set(srv, "session/prompt", SessionPrompt)
		jsonrpc.Set(srv, "session/cancel", SessionCancel)

		jsonrpc.Set(srv, "alk.cxykevin.top/session/get_background", SessionGetBackground)
		jsonrpc.Set(srv, "alk.cxykevin.top/session/get_effort", SessionGetEffort)

		jsonrpc.Set(srv, "alk.cxykevin.top/list_subagent", SubAgentList)

		jsonrpc.Set(srv, "alk.cxykevin.top/phrases/list", PhraseList)
	}

	{ // 文件系统
		jsonrpc.Set(srv, "alk.cxykevin.top/fs/stat", FsStat)
		jsonrpc.Set(srv, "alk.cxykevin.top/fs/read", FsRead)
		jsonrpc.Set(srv, "alk.cxykevin.top/fs/write", FsWrite)
		jsonrpc.Set(srv, "alk.cxykevin.top/fs/mkdir", FsMkdir)
		jsonrpc.Set(srv, "alk.cxykevin.top/fs/rm", FsRm)
		jsonrpc.Set(srv, "alk.cxykevin.top/fs/chmod", FsChmod)
		jsonrpc.Set(srv, "alk.cxykevin.top/fs/chown", FsChown)
	}

	// 自动 Telemetry：每月一次，异步执行、失败静默，不阻塞启动。
	go runAutoTelemetry()
}

// Close 关闭连接
// 连接断开后不会立即释放会话，而是启动延迟释放定时器
// 若在超时时间内有新的客户端重连，会话会继续正常运行
func Close(req any, call func(string, any, *string) error, connID uint64) (any, error) {
	bindedSessionOnConnMu.Lock()
	sessionIDs := bindedSessionOnConn[connID]
	delete(bindedSessionOnConn, connID)
	bindedSessionOnConnMu.Unlock()
	for _, sessionID := range sessionIDs {
		// 先移除连接绑定，再启动延迟释放定时器
		unregisterConnCall(connID, sessionID)
		scheduleSessionRelease(sessionID)
	}
	clientConnCapsMu.Lock()
	delete(clientConnCaps, connID)
	clientConnCapsMu.Unlock()
	return nil, nil
}
