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
	// MaskClientMessages 是否把发送给客户端的会话消息也替换为脱敏后的内容。
	// 默认关闭，客户端看到原文；开启后客户端看到的用户消息、AI 正文/思考、
	// 工具调用参数、历史回放与摘要都是同格式假值，便于演示与直播。
	// 仅影响发往客户端的内容：模型输入与本地数据库仍保留原文。
	// 依赖 Enable 总开关（映射表与出站脱敏共用）。
	MaskClientMessages bool `json:"mask_client_messages" default:"false"`
	// KeyMappingEvictEnable 是否自动淘汰过期的「原值 → 假值」映射，默认开启。
	// 关闭后 key_mappings 恢复"只增不删"的旧行为（不推荐：表会随会话历史无限增长）。
	KeyMappingEvictEnable bool `json:"key_mapping_evict_enable" default:"true"`
	// KeyMappingIdleHours 映射空闲多久后可被淘汰（小时，默认 24，下限 6）。
	// 语义：超过该时长既没出现在出站脱敏、也没新建，说明原值已不在发送窗口里，
	// 删掉不会改变仍会发送的内容，因此不会破坏 provider 的 prompt prefix cache。
	// 该值必须不小于所用模型的 prompt 缓存保留期（DeepSeek 3h、Anthropic 5m–1h）。
	KeyMappingIdleHours int `json:"key_mapping_idle_hours" default:"24"`
	// KeyMappingMaxRows 单库映射行数软上限（默认 5000）：只用于告警，
	// 不会越过 KeyMappingIdleHours 强删——热窗口内的行永远保留。
	KeyMappingMaxRows int `json:"key_mapping_max_rows" default:"5000"`
	// KeyMappingEvictBatch 单批删除行数（默认 500），控制单个事务大小。
	KeyMappingEvictBatch int `json:"key_mapping_evict_batch" default:"500"`
}
