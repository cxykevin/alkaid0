package structs

// RPCConfig ACP 协议（websocket 扩展）的配置
type RPCConfig struct {
	Host               string `default:"127.0.0.1"`
	Port               uint16 `default:"7433"`
	Key                string `default:""`
	Path               string `default:"/acp"`
	DisableStdioServer bool   `default:"false"`
}
