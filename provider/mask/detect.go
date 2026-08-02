package mask

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"sync"

	configStructs "github.com/cxykevin/alkaid0/config/structs"
)

// 敏感类型
const (
	TypeAPIKey  = "apikey"
	TypePhone   = "phone"
	TypeIP      = "ip"
	TypeSession = "session"
	TypeCookie  = "cookie"
	TypeJWT     = "jwt"
	// TypeCustom 用户通过 /mask add 手动指定的自定义脱敏值。
	TypeCustom = "custom"
)

// minBareKeyLen 无前缀裸 token 的最小长度。
// token 要么带已知前缀、要么非常长；阈值拉长以避免命中 git SHA-1(40)/SHA-256(64)/bcrypt(60) 等长哈希。
const minBareKeyLen = 80

// defaultIPWhitelist 内置的知名公共 DNS/NTP 服务 IP 白名单，这些公网 IP 默认不脱敏，
// 避免请求中出现常见基础设施地址时被误替换。用户仍可通过 mask_ip_whitelist 追加。
var defaultIPWhitelist = []string{
	// 公共 DNS
	"1.1.1.1", "1.0.0.1", // Cloudflare
	"8.8.8.8", "8.8.4.4", // Google
	"9.9.9.9",                            // Quad9
	"114.114.114.114", "114.114.115.115", // 114DNS
	"223.5.5.5", "223.6.6.6", // 阿里 DNS
	"119.29.29.29",         // DNSPod
	"180.76.76.76",         // 百度 DNS
	"101.6.6.6",            // 清华 TUNA DNS
	"1.2.4.8", "210.2.4.8", // CNNIC SDNS
	// 公共 NTP
	"203.107.6.88", // 阿里云 NTP
	"101.6.15.130", // 清华 TUNA NTP
}

// Span 表示文本中的一个敏感区间（半开区间 [Start, End)，按字节计）。
type Span struct {
	Start    int
	End      int
	Type     string
	Original string
}

// keyPrefixes 已知密钥前缀，检测时用于识别 apikey。
var keyPrefixes = []string{
	"sk-or-v1-", "sk-or-", "sk-", "sk_", "pk_", "AIza", "claude-", "xai-", "hf_",
	"gsk_", "alk-", "ak-", "nv-api-", "brx-", "qwen-", "pplx-", "key-", "app-",
	"secret-", "ghp_", "gho_", "gho-", "gocdk-", "gcp-", "gcs-", "gcs_", "cdk-", "cdk_",
}

// sortedPrefixes 按长度降序排列的前缀，用于取最长命中前缀。
var sortedPrefixes = func() []string {
	p := append([]string(nil), keyPrefixes...)
	sort.Slice(p, func(i, j int) bool { return len(p[i]) > len(p[j]) })
	return p
}()

var prefixKeyRe = sync.OnceValue(func() *regexp.Regexp {
	alt := strings.Join(sortedPrefixes, "|")
	return regexp.MustCompile(`\b(?:` + alt + `)[A-Za-z0-9_-]{8,}\b`)
})

var bareHexRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`\b[0-9a-fA-F]{%d,}\b`, minBareKeyLen))
})

var bareAlnumRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(`\b[A-Za-z0-9_-]{%d,}\b`, minBareKeyLen))
})

var phoneRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`\b1[3-9]\d{9}\b`)
})

// ipv4QuadRe 匹配点分十进制候选，再用 net.ParseIP + 上下文校验过滤。
var ipv4QuadRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?:[0-9]{1,3}\.){3}[0-9]{1,3}`)
})

// ipv6RunRe 匹配可能构成 IPv6 的「十六进制+冒号+点」连续段，再用 net.ParseIP 校验。
var ipv6RunRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`[0-9a-fA-F:.]{3,}`)
})

var sessionRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(?:session[_ -]?id|sessionid|session|sid|auth[_ ]?token|csrf[_ ]?token|token|cookie)\b\s*[=:]\s*([A-Za-z0-9_\-%]{6,})`)
})

var cookieHeaderRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?im)^\s*(?:Cookie|Set-Cookie):\s*[^;\r\n]+(?:;[^;\r\n]+)+`)
})

// cookiePairRe 匹配 Cookie 头内的 name=value 对。
var cookiePairRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`([^;=,\s]+)=([^;,]+)`)
})

var jwtRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
})

// detectSensitive 扫描文本中的敏感区间。maskToOrig 用于跳过已脱敏的假值。
func detectSensitive(text string, cfg *configStructs.DataMaskConfig, maskToOrig map[string]string) []Span {
	if text == "" {
		return nil
	}
	var spans []Span
	if cfg.MaskJWT {
		spans = append(spans, detectJWT(text)...)
	}
	if cfg.MaskSession {
		spans = append(spans, detectCookieHeader(text)...)
	}
	if cfg.MaskAPIKey {
		spans = append(spans, detectPrefixKey(text)...)
		spans = append(spans, detectBareKey(text)...)
	}
	if cfg.MaskSession {
		spans = append(spans, detectSessionValue(text)...)
	}
	if cfg.MaskPhone {
		spans = append(spans, detectPhone(text)...)
	}
	if cfg.MaskIP {
		spans = append(spans, detectIP(text, cfg)...)
	}
	return resolveSpans(spans, maskToOrig)
}

func detectPrefixKey(text string) []Span {
	var spans []Span
	for _, loc := range prefixKeyRe().FindAllStringIndex(text, -1) {
		s := text[loc[0]:loc[1]]
		spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeAPIKey, Original: s})
	}
	return spans
}

func detectBareKey(text string) []Span {
	var spans []Span
	for _, loc := range bareHexRe().FindAllStringIndex(text, -1) {
		s := text[loc[0]:loc[1]]
		if isHexKey(s) {
			spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeAPIKey, Original: s})
		}
	}
	for _, loc := range bareAlnumRe().FindAllStringIndex(text, -1) {
		s := text[loc[0]:loc[1]]
		spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeAPIKey, Original: s})
	}
	return spans
}

func detectPhone(text string) []Span {
	var spans []Span
	for _, loc := range phoneRe().FindAllStringIndex(text, -1) {
		s := text[loc[0]:loc[1]]
		spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypePhone, Original: s})
	}
	return spans
}

func detectIP(text string, cfg *configStructs.DataMaskConfig) []Span {
	var spans []Span
	for _, loc := range ipv4QuadRe().FindAllStringIndex(text, -1) {
		s := text[loc[0]:loc[1]]
		if !ipv4ContextOK(text, loc[0], loc[1]) {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil || !isPublicIP(ip) || ipWhitelisted(ip, cfg.MaskIPWhitelist) {
			continue
		}
		spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeIP, Original: s})
	}
	for _, loc := range ipv6RunRe().FindAllStringIndex(text, -1) {
		s := text[loc[0]:loc[1]]
		if !strings.Contains(s, ":") {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil || ip.To16() == nil || ip.To4() != nil || !isPublicIP(ip) || ipWhitelisted(ip, cfg.MaskIPWhitelist) {
			continue
		}
		spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeIP, Original: s})
	}
	return spans
}

// ipWhitelisted 判断 IP 是否命中白名单：先查内置的知名公共 DNS/NTP 白名单，
// 再查用户配置的 mask_ip_whitelist（支持单个 IP 或 CIDR，如 "1.1.1.1"/"8.8.8.0/24"）。
// 命中的公网 IP 不脱敏。
func ipWhitelisted(ip net.IP, configList []string) bool {
	if ip == nil {
		return false
	}
	return inIPList(ip, defaultIPWhitelist) || inIPList(ip, configList)
}

func inIPList(ip net.IP, list []string) bool {
	for _, w := range list {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(w); err == nil {
			if ipnet.Contains(ip) {
				return true
			}
			continue
		}
		if wip := net.ParseIP(w); wip != nil && wip.Equal(ip) {
			return true
		}
	}
	return false
}

// ipv4ContextOK 校验 IPv4 候选的上下文：前后不得紧邻数字或点（避免截取长点分串），
// 且不得紧跟 URL 斜杠或端口冒号（避免破坏 URL host 语义）。
func ipv4ContextOK(text string, start, end int) bool {
	if start > 0 {
		c := text[start-1]
		if isDigit(c) || c == '.' || c == '/' || c == ':' {
			return false
		}
	}
	if end < len(text) {
		c := text[end]
		if isDigit(c) {
			return false
		}
		if c == '.' {
			// 允许句子末尾的单个句号（后面不再紧跟数字）
			if end+1 < len(text) && isDigit(text[end+1]) {
				return false
			}
		}
	}
	return true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// detectSessionValue 匹配 session/sid/token/cookie 等关键字后的值（只替换值部分）。
func detectSessionValue(text string) []Span {
	var spans []Span
	for _, loc := range sessionRe().FindAllStringSubmatchIndex(text, -1) {
		if len(loc) < 4 || loc[2] < 0 {
			continue
		}
		vStart, vEnd := loc[2], loc[3]
		// 值后紧跟 '(' 说明是函数调用（如 token = getToken()），跳过避免破坏代码语义
		if vEnd < len(text) && text[vEnd] == '(' {
			continue
		}
		spans = append(spans, Span{Start: vStart, End: vEnd, Type: TypeSession, Original: text[vStart:vEnd]})
	}
	return spans
}

// detectCookieHeader 匹配整段 Cookie/Set-Cookie 头（≥2 个 name=value 对），只替换各值部分。
func detectCookieHeader(text string) []Span {
	var spans []Span
	for _, loc := range cookieHeaderRe().FindAllStringIndex(text, -1) {
		header := text[loc[0]:loc[1]]
		colon := strings.IndexByte(header, ':')
		if colon < 0 {
			continue
		}
		valuePart := header[colon+1:]
		base := loc[0] + colon + 1
		for _, m := range cookiePairRe().FindAllStringSubmatchIndex(valuePart, -1) {
			name := strings.TrimSpace(valuePart[m[2]:m[3]])
			if isCookieAttr(name) {
				continue
			}
			vStart, vEnd := m[4], m[5]
			raw := valuePart[vStart:vEnd]
			// 去掉包裹值的引号，避免假值破坏引号结构
			if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
				vStart++
				vEnd--
				raw = raw[1 : len(raw)-1]
			}
			if raw == "" {
				continue
			}
			spans = append(spans, Span{Start: base + vStart, End: base + vEnd, Type: TypeCookie, Original: raw})
		}
	}
	return spans
}

func detectJWT(text string) []Span {
	var spans []Span
	for _, loc := range jwtRe().FindAllStringIndex(text, -1) {
		s := text[loc[0]:loc[1]]
		if isJWT(s) {
			spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeJWT, Original: s})
		}
	}
	return spans
}

// detectCustom 精确匹配用户自定义脱敏值（/mask add 加入），生成 TypeCustom span。
// 自定义值可能重叠（如一个值包含另一个），交由 resolveSpans 消解。
func detectCustom(text string, customs []string) []Span {
	var spans []Span
	for _, v := range customs {
		if v == "" {
			continue
		}
		for i := 0; i < len(text); {
			idx := strings.Index(text[i:], v)
			if idx < 0 {
				break
			}
			start := i + idx
			spans = append(spans, Span{Start: start, End: start + len(v), Type: TypeCustom, Original: v})
			i = start + 1
		}
	}
	return spans
}

// resolveSpans 过滤已脱敏区间、排序（起点升序、同起点长者优先）、消解重叠。
func resolveSpans(spans []Span, maskToOrig map[string]string) []Span {
	filtered := make([]Span, 0, len(spans))
	for _, s := range spans {
		if _, ok := maskToOrig[s.Original]; ok {
			continue // 本身已是假值，不重复脱敏
		}
		filtered = append(filtered, s)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Start != filtered[j].Start {
			return filtered[i].Start < filtered[j].Start
		}
		return filtered[i].End > filtered[j].End
	})
	res := make([]Span, 0, len(filtered))
	lastEnd := -1
	for _, s := range filtered {
		if s.Start >= lastEnd {
			res = append(res, s)
			lastEnd = s.End
		}
	}
	return res
}

// isPublicIP 判定 IP 是否为公网地址（排除本地/回环/私有/链路本地/组播/保留）。
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && isReservedIPv4(v4) {
		return false
	}
	return true
}

// isReservedIPv4 覆盖 0.0.0.0/8、CGNAT、TEST-NET 等保留 IPv4 段。
func isReservedIPv4(ip net.IP) bool {
	v := ip.To4()
	if v == nil {
		return false
	}
	switch {
	case v[0] == 0: // 0.0.0.0/8
		return true
	case v[0] == 100 && v[1] >= 64 && v[1] <= 127: // 100.64.0.0/10 CGNAT
		return true
	case v[0] == 192 && v[1] == 0 && v[2] == 0: // 192.0.0.0/24
		return true
	case v[0] == 192 && v[1] == 0 && v[2] == 2: // TEST-NET-1 192.0.2.0/24
		return true
	case v[0] == 192 && v[1] == 88 && v[2] == 99: // 192.88.99.0/24
		return true
	case v[0] == 198 && (v[1] == 18 || v[1] == 19): // 198.18.0.0/15
		return true
	case v[0] == 198 && v[1] == 51 && v[2] == 100: // TEST-NET-2 198.51.100.0/24
		return true
	case v[0] == 203 && v[1] == 0 && v[2] == 113: // TEST-NET-3 203.0.113.0/24
		return true
	case v[0] == 255 && v[1] == 255 && v[2] == 255 && v[3] == 255: // 255.255.255.255
		return true
	}
	return false
}

// isJWT 校验三段点分 token 是否为 JWT：header 可 base64url 解码为含 alg 的 JSON。
func isJWT(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var h map[string]any
	if json.Unmarshal(hdr, &h) != nil {
		return false
	}
	if _, ok := h["alg"]; !ok {
		return false
	}
	if _, err := base64.RawURLEncoding.DecodeString(parts[1]); err != nil {
		return false
	}
	return true
}

// isCookieAttr 标准 Cookie 属性名（不脱敏）。
func isCookieAttr(name string) bool {
	switch strings.ToLower(name) {
	case "path", "domain", "expires", "max-age", "secure", "httponly", "samesite",
		"comment", "priority", "partition", "partioned", "version", "discard":
		return true
	}
	return false
}
