package actions

import (
	"fmt"

	"github.com/cxykevin/alkaid0/product"
	u "github.com/cxykevin/alkaid0/utils"
)

const protoVersion = 1

// InitializeRequest 初始化请求
type InitializeRequest struct {
	ProtocolVersion    int `json:"protocolVersion"`
	ClientCapabilities u.H `json:"clientCapabilities"`
	ClientInfo         struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

// InitializeResponse 初始化响应
type InitializeResponse struct {
	ProtocolVersion   int   `json:"protocolVersion"`
	AgentCapabilities u.H   `json:"agentCapabilities"`
	AgentInfo         u.H   `json:"agentInfo"`
	AuthMethods       []u.H `json:"authMethods"`
}

// AgentCapabilities 服务端能力常量
var AgentCapabilities = u.H{
	"loadSession": true,
	"promptCapabilities": u.H{
		"image":           false,
		"audio":           false,
		"embeddedContext": false,
	},
	"mcp":  false,
	"list": u.H{},
}

// AgentInfo 服务端信息常量
var AgentInfo = u.H{
	"name":    "alkaid0",
	"title":   "Alkaid0",
	"version": product.Version,
}

var clientConnCaps = map[uint64]u.H{}

// Initialize 初始化
func Initialize(req InitializeRequest, call func(string, any) error, connID uint64) (InitializeResponse, error) {
	if req.ProtocolVersion != protoVersion {
		return InitializeResponse{}, fmt.Errorf("protocol version not match")
	}
	clientConnCaps[connID] = req.ClientCapabilities
	return InitializeResponse{
		ProtocolVersion:   protoVersion,
		AgentCapabilities: AgentCapabilities,
		AgentInfo:         AgentInfo,
	}, nil
}
