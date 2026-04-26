package jsonrpc

import (
	"encoding/json"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"

	"github.com/cxykevin/alkaid0/server/client/jsonrpc/connect"
	u "github.com/cxykevin/alkaid0/utils"
)

var logger = log.New("server")

// Server jsonrpc 服务器
type Server struct {
	Methods map[string]func(u.H, func(string, any, *string) error, uint64) (any, error)
}

// // ConnIdx 连接 ID 索引
// var ConnIdx uint64 = 0

// closeConn 关闭连接
func (s *Server) closeConn(connID uint64) {
	fn, ok := s.Methods["_close"]
	if !ok {
		return
	}
	fn(nil, nil, connID)
}

// 判断 ID 是否为有效的非通知 ID
// 通知请求的 ID 为 nil 或缺失
func isNotification(id any) bool {
	return id == nil
}

// IgnoreReply 忽略回复
type IgnoreReply struct{}

// handle 处理请求
func (s *Server) handle(arg string, call func(string) error, connID uint64) (returnString string, exit bool) {
	// var req Request
	var retByte []byte
	solveSingleRequest := func(req Request) (returns *Response, exit bool) {
		logger.Info("handle %s in ConnID %d", req.Method, connID)
		// 检查是否为通知请求
		isNotif := isNotification(req.ID)

		if req.Version != JSONRPCVersion {
			if isNotif {
				// 通知请求的错误不返回响应
				return nil, false
			}
			return &Response{
				Version: JSONRPCVersion,
				ID:      req.ID,
				Error: &Error{
					Code:    JRPCInvalidRequest,
					Message: "invalid version",
				},
			}, false
		}
		if req.Method == "exit" {
			return nil, true
		}
		if req.Method == "ping" {
			if isNotif {
				// 通知请求不返回响应
				return nil, false
			}
			return &Response{
				Version: JSONRPCVersion,
				ID:      req.ID,
				Result:  "pong",
			}, false
		}
		fn, ok := s.Methods[req.Method]
		if !ok {
			if isNotif {
				// 通知请求的错误不返回响应
				return nil, false
			}
			return &Response{
				Version: JSONRPCVersion,
				ID:      req.ID,
				Error: &Error{
					Code:    JRPCMethodNotFound,
					Message: "\"" + req.Method + "\" not found",
				},
			}, false
		}
		obj, err := fn(req.Params, func(meth string, v any, id *string) error {
			var returnByte []byte
			var err error
			if id == nil {
				returnByte, err = json.Marshal(RequestWithoutID{
					Version: JSONRPCVersion,
					Method:  meth,
					Params:  u.Unwrap(u.ReApply(v)),
				})
			} else {
				returnByte, err = json.Marshal(Request{
					Version: JSONRPCVersion,
					ID:      *id,
					Method:  meth,
					Params:  u.Unwrap(u.ReApply(v)),
				})
			}
			if err != nil {
				return err
			}
			return call(string(returnByte))
		}, connID)
		if err != nil {
			if isNotif {
				// 通知请求的错误不返回响应
				return nil, false
			}
			return &Response{
				Version: JSONRPCVersion,
				ID:      req.ID,
				Error: &Error{
					Code:    JRPCServerError,
					Message: err.Error(),
				},
			}, false
		}

		if obj == nil {
			return nil, false
		}

		if isNotif {
			// 通知请求不返回响应
			return nil, false
		}
		return &Response{
			Version: JSONRPCVersion,
			ID:      req.ID,
			Result:  obj,
		}, false
	}
	var reqBatch []Request
	err := json.Unmarshal([]byte(arg), &reqBatch)
	if err == nil && len(reqBatch) > 0 {
		// 过滤掉通知请求，只返回非通知请求的响应
		rets := make([]Response, 0, len(reqBatch))
		for _, req := range reqBatch {
			retObj, exit := solveSingleRequest(req)
			if exit {
				return "", true
			}
			if retObj != nil {
				rets = append(rets, *retObj)
			}
		}
		if len(rets) > 0 {
			retByte, _ = json.Marshal(rets)
			return string(retByte), false
		}
		return "", false
	}
	var req Request
	err = json.Unmarshal([]byte(arg), &req)
	if err != nil {
		// 解析失败时，检查是否为通知
		// 注意：当 JSON 解析失败时，无法获取 ID，所以响应的 ID 应为 null
		retByte, _ := json.Marshal(Response{
			Version: JSONRPCVersion,
			ID:      nil, // 解析错误时 ID 为 null
			Error: &Error{
				Code:    JRPCParseError,
				Message: err.Error(),
			},
		})
		return string(retByte), false
	}
	retObj, exit := solveSingleRequest(req)
	if exit {
		return "", true
	}
	if retObj != nil {
		retByte, _ = json.Marshal(retObj)
		return string(retByte), false
	}
	// 通知请求或其他情况不返回任何内容
	return "", false
}

// Start 启动 jsonrpc 服务器（使用 stdio）
func (s *Server) Start() {
	if !config.GlobalConfig.Server.DisableStdioServer {
		connect.StartStdio(s.handle, s.closeConn)
	}
}

// StartWs 启动 WebSocket JSON-RPC 服务器
func (s *Server) StartWs() error {
	return connect.StartWs(s.handle, s.closeConn)
}

// Set 设置方法
func Set[T any, T2 any](s *Server, method string, function func(T, func(string, any, *string) error, uint64) (T2, error)) {
	logger.Debug("set method %s", method)
	s.Methods[method] = func(v u.H, f func(string, any, *string) error, id uint64) (any, error) {
		ret, err := function(u.Unwrap(u.Apply[T](v)), f, id)
		_, ok := any(ret).(IgnoreReply)
		if ok {
			return nil, nil
		}
		return any(ret), err
	}
}

// New 创建一个 jsonrpc 服务器
func New() *Server {
	return &Server{
		Methods: make(map[string]func(u.H, func(string, any, *string) error, uint64) (any, error)),
	}
}
