package actions

import (
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/stats"
)

// UsageRequest 查询全局 token 用量的请求（无需参数）。
type UsageRequest struct{}

// Usage 返回全局 token 用量统计快照。
// 私有 ACP 方法：alk.cxykevin.top/usage，供前端查询用量展示。
func Usage(_ UsageRequest, _ func(string, any, *string) error, _ uint64) (stats.Info, error) {
	return stats.Snapshot(), nil
}

// formatUsage 把统计快照渲染成 markdown 文本，供 /usage 命令展示。
func formatUsage(snap stats.Info) string {
	var b strings.Builder
	b.WriteString("**Token Usage:**\n")
	b.WriteString(fmt.Sprintf("  - Requests: %d\n", snap.Total.Requests))
	b.WriteString(fmt.Sprintf("  - Prompt: %d | Completion: %d | Cached: %d\n",
		snap.Total.PromptTokens, snap.Total.CompletionTokens, snap.Total.CachedTokens))
	b.WriteString(fmt.Sprintf("  - Cache Hit Ratio: %.2f%%\n", snap.Total.CacheHitRatio*100))
	if len(snap.Models) > 0 {
		b.WriteString("**By Model:**\n")
		for _, m := range snap.Models {
			b.WriteString(fmt.Sprintf("  - %s: prompt %d / completion %d / cached %d (%.2f%%)\n",
				m.ModelName, m.PromptTokens, m.CompletionTokens, m.CachedTokens, m.CacheHitRatio*100))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
