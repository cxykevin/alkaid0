package mask

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
)

// genFake 按类型生成与原值同长度、同字符集的假值。
// 生成结果不要求确定性（db 中的映射是唯一事实源）；仅要求格式与长度保持一致。
func genFake(original, typ string) string {
	switch typ {
	case TypePhone:
		return genPhone(original)
	case TypeIP:
		return genIP(original)
	case TypeJWT:
		return genJWT(original)
	default:
		return genGeneric(original)
	}
}

// genGeneric 通用假值：保留已知前缀，只替换主体。
func genGeneric(original string) string {
	prefix, body := detectPrefix(original)
	if prefix != "" {
		return prefix + genBody(body)
	}
	return genBody(original)
}

// detectPrefix 返回命中的已知前缀（最长）与主体。
func detectPrefix(original string) (prefix, body string) {
	for _, p := range sortedPrefixes {
		if strings.HasPrefix(original, p) {
			return p, original[len(p):]
		}
	}
	return "", original
}

// genBody 按主体格式生成假值：hex → hex，纯数字 → 数字，其余按字符类分段。
func genBody(s string) string {
	switch {
	case isHexKey(s):
		return genHexLike(s)
	case isDigits(s):
		return genDigits(len(s))
	default:
		return genByCharClass(s)
	}
}

// isHexKey 判断是否为「含字母」的 16 进制串（纯数字不算 hex，走数字分支）。
func isHexKey(s string) bool {
	if len(s) < 16 {
		return false
	}
	hasLetter := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
			hasLetter = true
		case c >= 'A' && c <= 'F':
			hasLetter = true
		default:
			return false
		}
	}
	return hasLetter
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// genHexLike 生成同长度 hex，保持每位的字母大小写形态。
func genHexLike(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			b[i] = byte('0' + rand.Intn(10))
		case s[i] >= 'a' && s[i] <= 'f':
			b[i] = byte('a' + rand.Intn(6))
		default: // A-F
			b[i] = byte('A' + rand.Intn(6))
		}
	}
	return string(b)
}

func genDigits(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('0' + rand.Intn(10))
	}
	return string(b)
}

// genByCharClass 按同字符类连续段生成：数字→数字、小写→小写、大写→大写、符号→原样保留。
func genByCharClass(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		j := i + 1
		for j < len(s) && charClass(s[j]) == charClass(s[i]) {
			j++
		}
		b = append(b, genClassRun(s[i:j])...)
		i = j
	}
	return string(b)
}

func charClass(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return 1
	case c >= 'a' && c <= 'z':
		return 2
	case c >= 'A' && c <= 'Z':
		return 3
	default:
		return 4
	}
}

func genClassRun(run string) string {
	switch charClass(run[0]) {
	case 1:
		return genDigits(len(run))
	case 2:
		b := make([]byte, len(run))
		for i := range b {
			b[i] = byte('a' + rand.Intn(26))
		}
		return string(b)
	case 3:
		b := make([]byte, len(run))
		for i := range b {
			b[i] = byte('A' + rand.Intn(26))
		}
		return string(b)
	default:
		return run // 分隔符/符号原样保留
	}
}

// genPhone 生成同长度 11 位手机号（首位 1、第二位 3-9）。
func genPhone(s string) string {
	if len(s) == 11 {
		b := []byte(s)
		b[0] = '1'
		b[1] = byte('3' + rand.Intn(7)) // 3-9
		for i := 2; i < 11; i++ {
			b[i] = byte('0' + rand.Intn(10))
		}
		return string(b)
	}
	return genBody(s)
}

// genIP 生成同族、同长度格式的合法公网 IP。
func genIP(s string) string {
	ip := net.ParseIP(s)
	if ip == nil {
		return genBody(s)
	}
	if v4 := ip.To4(); v4 != nil {
		parts := strings.Split(s, ".")
		if len(parts) == 4 {
			out := make([]string, 4)
			out[0] = fmt.Sprintf("%0*d", len(parts[0]), genPublicFirstOctet(len(parts[0])))
			for i := 1; i < 4; i++ {
				out[i] = fmt.Sprintf("%0*d", len(parts[i]), genOctet(len(parts[i])))
			}
			return strings.Join(out, ".")
		}
		return genBody(s)
	}
	return genIPv6Like(s)
}

// publicFirstOctets 可用的公网首八位组（排除私有/回环/链路本地/CGNAT/TEST-NET 等）。
var publicFirstOctets = func() []int {
	var res []int
	for v := 1; v <= 223; v++ {
		switch v {
		case 10, 100, 127, 169, 172, 192, 198:
			continue
		}
		res = append(res, v)
	}
	return res
}()

func numDigits(v int) int {
	if v == 0 {
		return 1
	}
	n := 0
	for v > 0 {
		v /= 10
		n++
	}
	return n
}

// genOctet 生成指定十进制位数的八位组值（1 位 1-9，2 位 10-99，3 位 100-255）。
func genOctet(digits int) int {
	minV, maxV := 0, 9
	switch digits {
	case 1:
		minV, maxV = 1, 9
	case 2:
		minV, maxV = 10, 99
	case 3:
		minV, maxV = 100, 255
	}
	return minV + rand.Intn(maxV-minV+1)
}

// genPublicFirstOctet 生成指定十进制位数的公网首八位组。
func genPublicFirstOctet(digits int) int {
	var pool []int
	for _, v := range publicFirstOctets {
		if numDigits(v) == digits {
			pool = append(pool, v)
		}
	}
	if len(pool) == 0 {
		return genOctet(digits)
	}
	return pool[rand.Intn(len(pool))]
}

// genIPv6Like 保留骨架（冒号与每段十六进制字符数），逐位随机 hex 并保持大小写形态。
func genIPv6Like(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
			b[i] = byte('0' + rand.Intn(10))
		case s[i] >= 'a' && s[i] <= 'f':
			b[i] = byte('a' + rand.Intn(6))
		case s[i] >= 'A' && s[i] <= 'F':
			b[i] = byte('A' + rand.Intn(6))
		default:
			b[i] = s[i] // 冒号等保留
		}
	}
	return string(b)
}

// genJWT 构造与原文 header+payload 相同、签名伪造的假 JWT（签名段同长度 base64url）。
func genJWT(s string) string {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return genBody(s)
	}
	h, p, sig := parts[0], parts[1], parts[2]
	return h + "." + p + "." + genBase64URL(len(sig))
}

const base64URLChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

func genBase64URL(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = base64URLChars[rand.Intn(len(base64URLChars))]
	}
	return string(b)
}
