package log

import (
	"regexp"
	"strings"
)

// SanitizeSensitiveInfo 自动脱敏API密钥和网址（全局静态配置）
// API密钥：保留前缀，替换为 sk-***, AIza***, claude-***, xai-***, hf_***, gsk_***, alk-***
func SanitizeSensitiveInfo(text string) string {
	if text == "" {
		return ""
	}

	// 静态前缀列表，直接写死在函数内
	const prefixPattern = `(?:sk-|AIza|claude-|xai-|hf_|gsk_|alk-|ak-|sk_|pk_|nv-api-|brx-|qwen-|pplx-|key-|app-|secret-|Bearer|ghp_|gocdk-|gcp-|gcs-|gcs_|cdk-|cdk_)`

	// 完整正则表达式
	pattern := `\b(` + prefixPattern + `)[A-Za-z0-9-_]{8,}\b|(https?://|www\.)[^/\s]+(/\S*)?`
	re := regexp.MustCompile(pattern)

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

	return strings.TrimSpace(result)
}
