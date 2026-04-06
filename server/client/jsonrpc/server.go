package jsonrpc

import (
	"encoding/json"

	"github.com/cxykevin/alkaid0/server/client/jsonrpc/connect"
	u "github.com/cxykevin/alkaid0/utils"
)

// Server jsonrpc 服务器
type Server struct {
	Methods map[string]func(u.H, func(string, any) error, uint64) (any, error)
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

// handle 处理请求
func (s *Server) handle(arg string, call func(string) error, connID uint64) (returnString string, exit bool) {
	// var req Request
	var retByte []byte
	solveSingleRequest := func(req Request) (returns *Response, exit bool) {
		if req.Version != JSONRPCVersion {
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
			return &Response{
				Version: JSONRPCVersion,
				ID:      req.ID,
				Result:  "pong",
			}, false
		}
		fn, ok := s.Methods[req.Method]
		if !ok {
			return &Response{
				Version: JSONRPCVersion,
				ID:      req.ID,
				Error: &Error{
					Code:    JRPCMethodNotFound,
					Message: "\"" + req.Method + "\" not found",
				},
			}, false
		}
		obj, err := fn(req.Params, func(meth string, v any) error {
			returnByte, err := json.Marshal(Request{
				Version: JSONRPCVersion,
				ID:      req.ID,
				Method:  meth,
				Params:  u.Unwrap(u.ReApply(v)),
			})
			if err != nil {
				return err
			}
			return call(string(returnByte))
		}, connID)
		if err != nil {
			return &Response{
				Version: JSONRPCVersion,
				ID:      req.ID,
				Error: &Error{
					Code:    JRPCServerError,
					Message: err.Error(),
				},
			}, false
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
		rets := make([]Response, len(reqBatch))
		for i, req := range reqBatch {
			retObj, exit := solveSingleRequest(req)
			if exit {
				return "", true
			}
			if retObj != nil {
				rets[i] = *retObj
			}
		}
		retByte, _ = json.Marshal(rets)
		return string(retByte), false
	}
	var req Request
	err = json.Unmarshal([]byte(arg), &req)
	if err != nil {
		retByte, _ := json.Marshal(Response{
			Version: JSONRPCVersion,
			ID:      req.ID,
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
	}
	return string(retByte), false
}

// Start 启动 jsonrpc 服务器
func (s *Server) Start() {
	connect.StartStdio(s.handle, s.closeConn)
}

// Set 设置方法
func Set[T any, T2 any](s *Server, method string, function func(T, func(string, any) error, uint64) (T2, error)) {
	s.Methods[method] = func(v u.H, f func(string, any) error, id uint64) (any, error) {
		ret, err := function(u.Unwrap(u.Apply[T](v)), f, id)
		return any(ret), err
	}
}

// New 创建一个 jsonrpc 服务器
func New() *Server {
	return &Server{
		Methods: make(map[string]func(u.H, func(string, any) error, uint64) (any, error)),
	}
}
