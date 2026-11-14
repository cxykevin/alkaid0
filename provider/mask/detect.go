package mask

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"net"
	"regexp"
	"slices"
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
	slices.SortFunc(p, func(a, b string) int { return len(b) - len(a) })
	return p
}()

// keyPrefixByFirst 按首字节分桶的前缀表。
//
// 前缀检测原先跑 `\b(?:sk-or-v1-|...|cdk_)[A-Za-z0-9_-]{8,}\b`：28 个分支的长交替让
// 正则引擎在每个字节位置都要尝试全部分支（90 KB 文本 10 ms 量级）。改成逐字节查桶、
// 命中前缀后再做一遍线性后缀校验，同一文本只要几十微秒，且零分配。
var keyPrefixByFirst = func() [256][]string {
	var t [256][]string
	for _, p := range sortedPrefixes {
		t[p[0]] = append(t[p[0]], p)
	}
	return t
}()

// 字符分类（ASCII），对应正则里的 \w / [0-9a-fA-F] / [A-Za-z0-9_-] / [0-9a-fA-F:.]。
func isLowerAlpha(c byte) bool { return c >= 'a' && c <= 'z' }

func isUpperAlpha(c byte) bool { return c >= 'A' && c <= 'Z' }

func isWordChar(c byte) bool {
	return c == '_' || isDigit(c) || isLowerAlpha(c) || isUpperAlpha(c)
}

func isHexChar(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// isTokenChar 等价于 [A-Za-z0-9_-]。
func isTokenChar(c byte) bool {
	return c == '-' || c == '_' || isDigit(c) || isLowerAlpha(c) || isUpperAlpha(c)
}

// isIPv6RunChar 等价于 [0-9a-fA-F:.]。
func isIPv6RunChar(c byte) bool {
	return c == ':' || c == '.' || isHexChar(c)
}

// lowerASCII 只折叠 ASCII 大写字母（与 (?i) 在 ASCII 文本上的效果一致）。
func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// containsFold 做 ASCII 大小写不敏感的 Contains，不分配。
// 用于 session/token 这类关键词的廉价预筛（Go 标准库的大小写不敏感匹配会先 ToLower）。
func containsFold(s, sub string) bool {
	n, m := len(s), len(sub)
	if m == 0 || m > n {
		return false
	}
	first := lowerASCII(sub[0])
	for i := 0; i+m <= n; i++ {
		if lowerASCII(s[i]) != first {
			continue
		}
		j := 1
		for ; j < m; j++ {
			if lowerASCII(s[i+j]) != lowerASCII(sub[j]) {
				break
			}
		}
		if j == m {
			return true
		}
	}
	return false
}

// ipv4QuadRe 是重构前 IPv4 候选所用的正则；生产路径已改为 detectIPv4 的手写扫描，
// 这里只保留给 detect_equiv_test 当参考实现。删掉它会让差分测试无法编译。
var ipv4QuadRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?:[0-9]{1,3}\.){3}[0-9]{1,3}`)
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

// detectSensitive 扫描文本中的敏感区间。maskToOrig 用于跳过已脱敏的假值。
func detectSensitive(text string, cfg *configStructs.DataMaskConfig, maskToOrig map[string]string) []Span {
	if text == "" {
		return nil
	}
	var spans []Span
	if cfg.MaskJWT {
		spans = append(spans, detectJWT(text)...)
	}
	// Cookie/Set-Cookie 头必然包含 "ookie"：正文里几乎不出现，先用一次廉价查找
	// 跳过整条多行正则（它对每个字节位置都要尝试 ^\s* 锚点）。
	if cfg.MaskSession && strings.Contains(text, "ookie") {
		spans = append(spans, detectCookieHeader(text)...)
	}
	if cfg.MaskAPIKey {
		spans = append(spans, detectPrefixKey(text)...)
		spans = append(spans, detectBareKey(text)...)
	}
	// session 正则要求「关键词 + [=:] + 值」；先用两次廉价判断（必须出现 =/:，且必须
	// 出现关键词）跳过大小写不敏感的多分支正则 —— 普通正文既没有 =/:，也没有这些词。
	if cfg.MaskSession &&
		(strings.IndexByte(text, '=') >= 0 || strings.IndexByte(text, ':') >= 0) &&
		(containsFold(text, "session") || containsFold(text, "sid") ||
			containsFold(text, "token") || containsFold(text, "cookie")) {
		spans = append(spans, detectSessionValue(text)...)
	}
	if cfg.MaskPhone {
		spans = append(spans, detectPhone(text)...)
	}
	// 不含 '.' 也不含 ':' 的文本不可能出现 IP。
	if cfg.MaskIP && (strings.IndexByte(text, '.') >= 0 || strings.IndexByte(text, ':') >= 0) {
		spans = append(spans, detectIP(text, cfg)...)
	}
	return resolveSpans(spans, maskToOrig)
}

// detectPrefixKey 识别带已知前缀的 key。
//
// 原实现是一条 28 分支的长交替正则 `\b(?:sk-or-v1-|...|cdk_)[A-Za-z0-9_-]{8,}\b`，
// 每个字节位置都要尝试全部分支。这里改为逐字节查首字符桶 + 线性后缀校验，语义等价：
// 前缀取最长、匹配不重叠、两端都要落在 \b 上。
func detectPrefixKey(text string) []Span {
	var spans []Span
	n := len(text)
	for i := 0; i < n; i++ {
		pfxLen := 0
		for _, p := range keyPrefixByFirst[text[i]] {
			if strings.HasPrefix(text[i:], p) {
				pfxLen = len(p)
				break
			}
		}
		if pfxLen == 0 {
			continue
		}
		// 前缀前的 \b：前一个字符不能是单词字符。
		if i > 0 && isWordChar(text[i-1]) {
			continue
		}
		body := i + pfxLen
		end := body
		for end < n && isTokenChar(text[end]) {
			end++
		}
		// 结尾的 \b 要求最后一个字符是 \w：贪婪吃满后把尾部 '-' 回溯掉。
		for end > body && !isWordChar(text[end-1]) {
			end--
		}
		if end-body < 8 {
			continue
		}
		spans = append(spans, Span{Start: i, End: end, Type: TypeAPIKey, Original: text[i:end]})
		i = end - 1 // 与 FindAllStringIndex 一致：匹配之间不重叠
	}
	return spans
}

// detectBareKey 识别没有前缀、但足够长的裸 token。
//
// 原实现跑两条正则（hex 80+ / [A-Za-z0-9_-] 80+）。后者是前者的超集：hex 字符都属于
// [A-Za-z0-9_-]，最长 run 的两端必然落在 \b 上（run 边界外不可能是 \w 字符），
// 同起点时 resolveSpans 只保留更长的那条 —— 所以最终结果等于「alnum 那条」。
// 这里直接按最长 run 扫描，比两条正则各跑一遍快得多，且命中与否都是零分配。
func detectBareKey(text string) []Span {
	var spans []Span
	n := len(text)
	for i := 0; i < n; {
		if !isTokenChar(text[i]) {
			i++
			continue
		}
		start := i
		for i < n && isTokenChar(text[i]) {
			i++
		}
		end := i
		// '-' 不是 \w：落在两端时正则的 \b 会回溯掉它们。
		for start < end && text[start] == '-' {
			start++
		}
		for end > start && text[end-1] == '-' {
			end--
		}
		if end-start < minBareKeyLen {
			continue
		}
		spans = append(spans, Span{Start: start, End: end, Type: TypeAPIKey, Original: text[start:end]})
	}
	return spans
}

// detectPhone 识别中国大陆手机号（等价于 `\b1[3-9]\d{9}\b`）。
// 固定 11 位数字用手写扫描比正则快得多：切出数字 run 后校验长度与两侧边界即可。
func detectPhone(text string) []Span {
	var spans []Span
	n := len(text)
	for i := 0; i < n; i++ {
		if !isDigit(text[i]) {
			continue
		}
		start := i
		for i < n && isDigit(text[i]) {
			i++
		}
		if i-start != 11 {
			continue
		}
		// 两端 \b：数字 run 两侧不能紧邻单词字符（字母/数字/下划线）。
		if start > 0 && isWordChar(text[start-1]) {
			continue
		}
		if i < n && isWordChar(text[i]) {
			continue
		}
		if text[start] != '1' || text[start+1] < '3' || text[start+1] > '9' {
			continue
		}
		spans = append(spans, Span{Start: start, End: i, Type: TypePhone, Original: text[start:i]})
	}
	return spans
}

func detectIP(text string, cfg *configStructs.DataMaskConfig) []Span {
	spans := detectIPv4(text, cfg)
	// 没有冒号就不可能有 IPv6。
	if strings.IndexByte(text, ':') < 0 {
		return spans
	}
	// IPv6 候选是 `[0-9a-fA-F:.]{3,}` 的连续段：原先交给正则，而英文正文里 a-f 字母与
	// 点号极多，正则要在几乎每个字节位置尝试（90 KB 文本十几毫秒）。改成一次线性扫描。
	n := len(text)
	for i := 0; i < n; {
		if !isIPv6RunChar(text[i]) {
			i++
			continue
		}
		start := i
		for i < n && isIPv6RunChar(text[i]) {
			i++
		}
		if i-start < 3 {
			continue
		}
		s := text[start:i]
		if !strings.Contains(s, ":") {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil || ip.To16() == nil || ip.To4() != nil || !isPublicIP(ip) || ipWhitelisted(ip, cfg.MaskIPWhitelist) {
			continue
		}
		spans = append(spans, Span{Start: start, End: i, Type: TypeIP, Original: s})
	}
	return spans
}

// detectIPv4 手写扫描 IPv4 候选（等价于 `(?:[0-9]{1,3}\.){3}[0-9]{1,3}` 的非重叠匹配）。
// 候选的推进与 FindAllStringIndex 一致：一旦按「四段数字」切成候选就跳过其内部，
// 上下文校验（ipv4ContextOK）只决定是否采用，不影响推进位置。
func detectIPv4(text string, cfg *configStructs.DataMaskConfig) []Span {
	var spans []Span
	n := len(text)
	for i := 0; i < n; i++ {
		if !isDigit(text[i]) {
			continue
		}
		start := i
		end, ok := matchIPv4Quad(text, start)
		if !ok {
			continue
		}
		i = end - 1 // 与 FindAllStringIndex 一致：匹配之间不重叠
		s := text[start:end]
		if !ipv4ContextOK(text, start, end) {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil || !isPublicIP(ip) || ipWhitelisted(ip, cfg.MaskIPWhitelist) {
			continue
		}
		spans = append(spans, Span{Start: start, End: end, Type: TypeIP, Original: s})
	}
	return spans
}

// matchIPv4Quad 从 start 起匹配 `d.d.d.d`（每段 1-3 位数字），返回结束下标。
func matchIPv4Quad(text string, start int) (int, bool) {
	n := len(text)
	j := start
	for seg := 0; seg < 4; seg++ {
		k := j
		for k < n && k-j < 3 && isDigit(text[k]) {
			k++
		}
		if k == j {
			return 0, false
		}
		if seg < 3 {
			if k >= n || text[k] != '.' {
				return 0, false
			}
			j = k + 1
		} else {
			j = k
		}
	}
	return j, true
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

// detectJWT 识别三段式 JWT（等价于 `[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`）。
//
// 该正则没有 \b 且三段都是「贪婪取最长 run」，所以手写扫描一次即可：按 run 切分，
// 依次要求「长度 ≥8 + 后接 '.'」。英文正文里点号密集，正则每次都要回溯，这里是 O(n)。
func detectJWT(text string) []Span {
	var spans []Span
	n := len(text)
	i := 0
	for i < n {
		if !isTokenChar(text[i]) {
			i++
			continue
		}
		start := i
		for i < n && isTokenChar(text[i]) {
			i++
		}
		if i-start < 8 || i >= n || text[i] != '.' {
			continue // i 停在 '.' 或 run 末尾，下一轮自然跳过非 token 字符
		}
		d1 := i
		j := d1 + 1
		seg2 := j
		for j < n && isTokenChar(text[j]) {
			j++
		}
		if j-seg2 < 8 || j >= n || text[j] != '.' {
			i = j
			continue
		}
		d2 := j
		k := d2 + 1
		seg3 := k
		for k < n && isTokenChar(text[k]) {
			k++
		}
		if k-seg3 < 8 {
			i = k
			continue
		}
		s := text[start:k]
		if isJWT(s) {
			spans = append(spans, Span{Start: start, End: k, Type: TypeJWT, Original: s})
		}
		i = k // 与 FindAllStringIndex 一致：匹配之间不重叠
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
//
// 全程原地操作：过滤与消解都把结果写回同一个底层数组（写指针始终不超前读指针），
// 因此除了排序本身之外不再分配中间切片。spans 由各 detector 拼接而来，调用方
// 用完即弃，原地改写是安全的。
func resolveSpans(spans []Span, maskToOrig map[string]string) []Span {
	filtered := spans[:0]
	for _, s := range spans {
		if _, ok := maskToOrig[s.Original]; ok {
			continue // 本身已是假值，不重复脱敏
		}
		filtered = append(filtered, s)
	}
	if len(filtered) < 2 {
		return filtered
	}
	slices.SortFunc(filtered, func(a, b Span) int {
		if a.Start != b.Start {
			return cmp.Compare(a.Start, b.Start)
		}
		return cmp.Compare(b.End, a.End) // 同起点长者优先
	})
	res := filtered[:0]
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
