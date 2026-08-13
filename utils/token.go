package u

import "unicode"

// EstimateTokens 粗略估算文本的 token 数（启发式，非精确，仅用于成本比较）。
// CJK 字符（中日韩）约 1 token/字，其余字符按约 4 字符/token 折中；非空文本至少返回 1。
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk := 0
	total := 0
	for _, r := range s {
		total++
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Hangul, r) {
			cjk++
		}
	}
	n := cjk + (total-cjk+3)/4
	if n <= 0 {
		return 1
	}
	return n
}
