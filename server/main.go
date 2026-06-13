// Package server 提供 WebSocket 服务端入口与客户端管理
package server

import "github.com/cxykevin/alkaid0/server/client"

// Start 启动服务
func Start() {
	client.Start()
}
