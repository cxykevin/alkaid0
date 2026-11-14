package mask

import (
	"fmt"
	"math/rand"
	"net"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	configStructs "github.com/cxykevin/alkaid0/config/structs"
)

// 本文件把「重构前的正则实现」保留为参考实现，用随机文本做差分测试。
//
// 检测逻辑从正则改成了手写扫描（为了把出站脱敏的 CPU 从每秒几 MB 拉回来），
// 这类改动的风险全在边界语义上：\b 落在哪、贪婪回溯到哪、匹配是否重叠。
// 这里逐样本比较两条实现的最终 spans（含 resolveSpans 的排序与消解），
// 任何语义漂移都会立刻失败。

var (
	refPrefixKeyRe = regexp.MustCompile(`\b(?:` + strings.Join(sortedPrefixes, "|") + `)[A-Za-z0-9_-]{8,}\b`)
	refBareHexRe   = regexp.MustCompile(`\b[0-9a-fA-F]{80,}\b`)
	refBareAlnumRe = regexp.MustCompile(`\b[A-Za-z0-9_-]{80,}\b`)
	refPhoneRe     = regexp.MustCompile(`\b1[3-9]\d{9}\b`)
	refJWTRe       = regexp.MustCompile(`[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	refIPv6RunRe   = regexp.MustCompile(`[0-9a-fA-F:.]{3,}`)
)

// refResolveSpans 是重构前的 resolveSpans（两个中间切片 + sort.Slice）。
func refResolveSpans(spans []Span, maskToOrig map[string]string) []Span {
	filtered := make([]Span, 0, len(spans))
	for _, s := range spans {
		if _, ok := maskToOrig[s.Original]; ok {
			continue
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

// refDetectIP 是重构前的 IP 检测（IPv4/IPv6 都由正则给出候选）。
func refDetectIP(text string, cfg *configStructs.DataMaskConfig) []Span {
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
	for _, loc := range refIPv6RunRe.FindAllStringIndex(text, -1) {
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

// refDetectSensitive 是重构前的 detectSensitive（正则版），cookie/session 两段未改动，直接复用。
func refDetectSensitive(text string, cfg *configStructs.DataMaskConfig, maskToOrig map[string]string) []Span {
	if text == "" {
		return nil
	}
	var spans []Span
	if cfg.MaskJWT {
		for _, loc := range refJWTRe.FindAllStringIndex(text, -1) {
			s := text[loc[0]:loc[1]]
			if isJWT(s) {
				spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeJWT, Original: s})
			}
		}
	}
	if cfg.MaskSession {
		spans = append(spans, detectCookieHeader(text)...)
	}
	if cfg.MaskAPIKey {
		for _, loc := range refPrefixKeyRe.FindAllStringIndex(text, -1) {
			s := text[loc[0]:loc[1]]
			spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeAPIKey, Original: s})
		}
		for _, loc := range refBareHexRe.FindAllStringIndex(text, -1) {
			s := text[loc[0]:loc[1]]
			if isHexKey(s) {
				spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeAPIKey, Original: s})
			}
		}
		for _, loc := range refBareAlnumRe.FindAllStringIndex(text, -1) {
			s := text[loc[0]:loc[1]]
			spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypeAPIKey, Original: s})
		}
	}
	if cfg.MaskSession {
		spans = append(spans, detectSessionValue(text)...)
	}
	if cfg.MaskPhone {
		for _, loc := range refPhoneRe.FindAllStringIndex(text, -1) {
			s := text[loc[0]:loc[1]]
			spans = append(spans, Span{Start: loc[0], End: loc[1], Type: TypePhone, Original: s})
		}
	}
	if cfg.MaskIP {
		spans = append(spans, refDetectIP(text, cfg)...)
	}
	return refResolveSpans(spans, maskToOrig)
}

// genDetectSamples 生成覆盖各检测器边界形态的随机文本（固定种子，可复现）。
func genDetectSamples(r *rand.Rand, n int) []string {
	noise := []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.:=/ ;,%\t\n")
	tokenChars := "abcXYZ019-_"
	hexChars := "abcdef0123456789-_"
	samples := make([]string, 0, n)
	for range n {
		var b strings.Builder
		for parts := r.Intn(12); parts > 0; parts-- {
			switch r.Intn(11) {
			case 0: // 纯噪声
				for range r.Intn(80) {
					b.WriteByte(noise[r.Intn(len(noise))])
				}
			case 1: // 带前缀的 key（前后可能紧邻字母，用于探测 \b）
				if r.Intn(4) == 0 {
					b.WriteString("x")
				}
				b.WriteString(sortedPrefixes[r.Intn(len(sortedPrefixes))])
				for range 8 + r.Intn(30) {
					b.WriteByte(tokenChars[r.Intn(len(tokenChars))])
				}
				if r.Intn(3) == 0 {
					b.WriteByte('-')
				}
				if r.Intn(4) == 0 {
					b.WriteString("y")
				}
			case 2: // 长裸 token（可能带首尾 '-'）
				for range 60 + r.Intn(60) {
					b.WriteByte(hexChars[r.Intn(len(hexChars))])
				}
			case 3: // 手机号形状
				b.WriteString(fmt.Sprintf("1%d%09d", 3+r.Intn(7), r.Intn(1_000_000_000)))
			case 4: // IPv4 形状
				b.WriteString(fmt.Sprintf("%d.%d.%d.%d", r.Intn(256), r.Intn(256), r.Intn(256), r.Intn(256)))
			case 5: // IPv6 形状
				b.WriteString(fmt.Sprintf("%x:%x::%x", r.Intn(65536), r.Intn(65536), r.Intn(65536)))
			case 6: // JWT 形状（三段长度随机，含恰好 7/8 的边界）
				b.WriteString(strings.Repeat("a", 4+r.Intn(8)))
				b.WriteString(".")
				b.WriteString(strings.Repeat("b", 4+r.Intn(8)))
				b.WriteString(".")
				b.WriteString(strings.Repeat("c", 4+r.Intn(8)))
			case 7: // Cookie 头
				b.WriteString("\nCookie: session=abc123def456; theme=dark\n")
			case 8: // session 赋值
				b.WriteString("session=" + strings.Repeat("x", 6+r.Intn(10)) + " ")
			case 9: // 点分但非 IP
				b.WriteString("v1.2.3.4.5.6")
			case 10:
				b.WriteString(" ")
			}
		}
		samples = append(samples, b.String())
	}
	return samples
}

// TestDetectSensitiveMatchesReference 保证手写检测与旧正则实现完全等价（默认配置）。
func TestDetectSensitiveMatchesReference(t *testing.T) {
	r := rand.New(rand.NewSource(20261114))
	for i, text := range genDetectSamples(r, 3000) {
		got := detectSensitive(text, fullCfg(), nil)
		want := refDetectSensitive(text, fullCfg(), nil)
		if !slices.Equal(got, want) {
			t.Fatalf("sample %d mismatch\ntext=%q\ngot  =%v\nwant =%v", i, text, got, want)
		}
	}
}

// TestDetectSensitiveMatchesReferenceWithMaskToOrig 覆盖「已脱敏假值不重复脱敏」的过滤分支。
func TestDetectSensitiveMatchesReferenceWithMaskToOrig(t *testing.T) {
	cfg := fullCfg()
	known := map[string]string{
		"sk-abcdefgh12345678": "sk-ZZZZZZZZ12345678",
		"13800138000":         "13900000000",
		"45.45.45.45":         "46.46.46.46",
	}
	r := rand.New(rand.NewSource(7))
	samples := append(genDetectSamples(r, 200), "key sk-abcdefgh12345678 phone 13800138000 ip 45.45.45.45")
	for i, text := range samples {
		got := detectSensitive(text, cfg, known)
		want := refDetectSensitive(text, cfg, known)
		if !slices.Equal(got, want) {
			t.Fatalf("sample %d mismatch\ntext=%q\ngot  =%v\nwant =%v", i, text, got, want)
		}
	}
}
