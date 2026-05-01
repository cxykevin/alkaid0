package structs

// RPCConfig ACP 协议（websocket 扩展）的配置
type RPCConfig struct {
	Host               string `json:"host" default:"127.0.0.1"`
	Port               uint16 `json:"port" default:"7433"`
	Key                string `json:"key" default:""`
	Path               string `json:"path" default:"/acp"`
	DisableStdioServer bool   `json:"disableStdioServer" default:"false"`
}
