package structs

// RPCConfig ACP 协议（websocket 扩展）的配置
type RPCConfig struct {
	Host               string `json:"host" default:"127.0.0.1"`
	Port               uint16 `json:"port" default:"7433"`
	Key                string `json:"key" default:""`
	Path               string `json:"path" default:"/acp"`
	DisableStdioServer bool   `json:"disableStdioServer" default:"false"`
	// SessionTimeout 连接断开后等待客户端重连的超时时间（秒），默认 60
	// 超时后将释放会话及其所有资源（loop、DB 连接）
	SessionTimeout int `json:"sessionTimeout" default:"60"`
}
