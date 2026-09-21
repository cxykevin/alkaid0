package jsonrpc

import "fmt"

// Error Codes
const (
	JRPCParseError     = -32700
	JRPCInvalidRequest = -32600
	JRPCMethodNotFound = -32601
	JRPCInvalidParams  = -32602
	JRPCInternalError  = -32603
	// JRPCServerError 是未指定错误码时的默认值（docs/acp/fs.md 记录的 -32099），
	// handler 返回 *RPCError 时可覆盖为更精确的协议错误码。
	JRPCServerError = -32099
)

// RPCError 携带具体 JSON-RPC 错误码的错误。
// handler 返回 *RPCError 时框架按 Code 回包；返回普通 error 时仍按默认的
// JRPCServerError（-32099）回包，保持既有协议与文档兼容。
type RPCError struct {
	Code    int
	Message string
}

// Error 实现 error 接口。
func (e *RPCError) Error() string { return e.Message }

// NewRPCError 构造带错误码的 RPC 错误。
func NewRPCError(code int, format string, args ...any) *RPCError {
	return &RPCError{Code: code, Message: fmt.Sprintf(format, args...)}
}
