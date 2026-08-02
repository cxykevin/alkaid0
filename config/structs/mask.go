package structs

// DataMaskConfig 安全 key 上下文（敏感数据出站脱敏）配置。
// 请求发送给 LLM 前，将 apikey/手机号/公网 IP/session/cookie/JWT 替换为
// 同格式假值，并在流式响应中用 AC 自动机还原为原文。
// 默认仅脱敏 apikey（含长 token）与 JWT；手机号/公网 IP/session/cookie 默认关闭，
// 避免误伤常见文本，需要时按类型打开。
type DataMaskConfig struct {
	// Enable 总开关，默认开启。
	Enable bool `json:"enable" default:"true"`
	// MaskAPIKey 是否脱敏 apikey（带已知前缀的 key 与超长裸 token，含长 token）。
	MaskAPIKey bool `json:"mask_api_key" default:"true"`
	// MaskPhone 是否脱敏中国大陆手机号（11 位）。默认关闭。
	MaskPhone bool `json:"mask_phone" default:"false"`
	// MaskIP 是否脱敏非本地/回环/私有的公网 IP。默认关闭。
	MaskIP bool `json:"mask_ip" default:"false"`
	// MaskIPWhitelist 公网 IP 白名单（单个 IP 或 CIDR，如 "1.1.1.1"/"8.8.8.0/24"），
	// 命中的 IP 即使为公网也不脱敏。常用于保留公共 DNS 等知名地址。
	MaskIPWhitelist []string `json:"mask_ip_whitelist,omitempty"`
	// MaskSession 是否脱敏 session/cookie（关键字后的值与整段 Cookie 头）。默认关闭。
	MaskSession bool `json:"mask_session_cookie" default:"false"`
	// MaskJWT 是否脱敏 JWT（构造同 header+payload、签名伪造的假 JWT）。
	MaskJWT bool `json:"mask_jwt" default:"true"`
}
