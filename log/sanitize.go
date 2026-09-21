package log

import (
	"regexp"
	"strings"
	"sync"
)

// 静态前缀列表
const prefixPattern = `(?:sk-|AIza|claude-|xai-|hf_|gsk_|alk-|ak-|sk_|pk_|nv-api-|brx-|qwen-|pplx-|key-|app-|secret-|Bearer|ghp_|gocdk-|gcp-|gcs-|gcs_|cdk-|cdk_)`

// 完整正则表达式：匹配 API 密钥或网址
const sensitivePattern = `\b(` + prefixPattern + `)[A-Za-z0-9-_]{8,}\b|(https?://|www\.)[^/\s]+(/\S*)?`

var sensitiveRegex = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(sensitivePattern)
})

// bearerRegex 匹配 "Bearer <token>"（大小写不敏感，允许任意空白分隔）。
// 旧规则里 "Bearer" 只是密钥前缀之一，必须紧贴 token 且无空白才算匹配，
// 而真实日志几乎总是 "Authorization: Bearer eyJhbGci..."（有空格），
// JWT 之类又没有固定前缀，于是整个 token 原样落盘。
var bearerRegex = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(bearer)(\s+)([A-Za-z0-9\-._~+/=]{8,})`)
})

// credPairRegex 匹配 URL/日志里的凭据查询参数（?key=...、&access_token=... 等）。
// 旧规则只掩盖 URL 的 host 部分，"https://host/v1?key=abc123" 会以
// "https://***/v1?key=abc123" 落盘——path 与 query 里的凭据原样泄露。
var credPairRegex = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access[_-]?token|auth|key|password|passwd|pwd|secret|sig|signature|token)=)([^&\s#]+)`)
})

// SanitizeSensitiveInfo 自动脱敏API密钥和网址（全局静态配置）
// API密钥：保留前缀，替换为 sk-***, AIza***, claude-***, xai-***, hf_***, gsk_***, alk-***
func SanitizeSensitiveInfo(text string) string {
	if text == "" {
		return ""
	}

	re := sensitiveRegex()

	// 执行替换
	result := re.ReplaceAllStringFunc(text, func(match string) string {
		submatches := re.FindStringSubmatch(match)
		if len(submatches) == 0 {
			return match
		}

		// 情况1: 匹配到API密钥
		if submatches[1] != "" {
			return submatches[1] + "***"
		}

		// 情况2: 匹配到网址
		protocol := submatches[2] // https:// 或 www.
		path := submatches[3]     // /path 部分，可能为空

		if path != "" {
			return protocol + "***" + path
		}
		return protocol + "***"
	})

	// 第二遍：处理带空白分隔的 Bearer 认证头
	result = bearerRegex().ReplaceAllString(result, "$1$2***")

	// 第三遍：掩盖 URL/日志里凭据查询参数的值（保留参数名，便于排查）
	result = credPairRegex().ReplaceAllString(result, "$1***")

	return strings.TrimSpace(result)
}
