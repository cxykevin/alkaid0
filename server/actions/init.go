package actions

import (
	"fmt"
	"sync"

	"github.com/cxykevin/alkaid0/product"
	u "github.com/cxykevin/alkaid0/utils"
)

const protoVersion = 2

// InitializeRequest 初始化请求（ACP v2：客户端与服务端共享 capabilities + info）
type InitializeRequest struct {
	ProtocolVersion int `json:"protocolVersion"`
	Capabilities    u.H `json:"capabilities"`
	Info            u.H `json:"info"`
}

// InitializeResponse 初始化响应（ACP v2：客户端与服务端共享 capabilities + info）
type InitializeResponse struct {
	ProtocolVersion int   `json:"protocolVersion"`
	Capabilities    u.H   `json:"capabilities"`
	Info            u.H   `json:"info"`
	AuthMethods     []u.H `json:"authMethods"`
}

// AgentCapabilities 服务端能力常量（ACP v2：能力标记为 {} 对象而非布尔。
// list/resume/close/prompt/cancel/update 为 session 基线无需标记；delete 为可选扩展）
var AgentCapabilities = u.H{
	"session": u.H{
		"prompt": u.H{
			"image":           u.H{},
			"embeddedContext": u.H{},
		},
		"delete": u.H{},
	},
	// alkaid0 扩展能力：服务器支持的 alkaid0 扩展协议版本
	"alk.cxykevin.top/alkaid0/v0.4": u.H{},
}

// AgentInfo 服务端信息常量
var AgentInfo = u.H{
	"name":    "alkaid0",
	"title":   "Alkaid0",
	"version": product.Version,
}

var (
	clientConnCapsMu sync.RWMutex
	clientConnCaps   = map[uint64]u.H{}
)

// Initialize 初始化
func Initialize(req InitializeRequest, call func(string, any, *string) error, connID uint64) (InitializeResponse, error) {
	if req.ProtocolVersion != protoVersion {
		return InitializeResponse{}, fmt.Errorf("protocol version not match")
	}
	clientConnCapsMu.Lock()
	clientConnCaps[connID] = req.Capabilities
	clientConnCapsMu.Unlock()
	return InitializeResponse{
		ProtocolVersion: protoVersion,
		Capabilities:    AgentCapabilities,
		Info:            AgentInfo,
		AuthMethods:     []u.H{},
	}, nil
}
