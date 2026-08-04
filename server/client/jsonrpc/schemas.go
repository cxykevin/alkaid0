package jsonrpc

import u "github.com/cxykevin/alkaid0/utils"

// JSONRPCVersion JSON-RPC 版本号
const JSONRPCVersion = "2.0"

// Request JSON-RPC 请求结构体
type Request struct {
	Version string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  u.H    `json:"params"`
}

// RequestWithoutID JSON-RPC 请求结构体
type RequestWithoutID struct {
	Version string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  u.H    `json:"params"`
}

// Error JSON-RPC 错误结构体
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Response JSON-RPC 响应结构体
type Response struct {
	Version string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// InboundMessage 入站消息结构，兼收请求与响应。
// 响应（有 id、无 method，携带 result/error）用于路由回等待中的出站请求（如 request_permission）。
type InboundMessage struct {
	Version string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  u.H    `json:"params"`
	Result  any    `json:"result"`
	Error   *Error `json:"error"`
}
