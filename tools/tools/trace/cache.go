package trace

import (
	"strconv"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// DiffPlan 单个 traced 文件的缓存决策结果（跨 build 链路传递，存于 TempKeyTraceDiffPlan）。
// Keep=true 仅为「方案2 候选」：硬性条件（超时/空/无变化/diff 超原文件）已排除，
// 软条件（成本比较）在 insert 阶段用真实 betweenTok 经 KeepDiffPlan 复核。
type DiffPlan struct {
	Keep      bool      // true=方案2 候选（保留旧块 + diff）；false=方案1（破坏缓存，走最新块）
	OldBlock  FileBlock // Keep=true 时有效：旧内容块（用 LastContent 渲染，字节与上次一致）
	DiffBlock FileBlock // Keep=true 时有效：diff 块（unified diff 文本）
	oTok      int       // 旧内容估算 token
	nTok      int       // 新内容估算 token
	dTok      int       // diff 文本估算 token
	mult      float32   // 缓存命中倍率（当前模型 CachePriceMultiplier）
}

const (
	cacheDefaultMultiplier float32 = 0.2  // 缓存命中 token 相对输入价格的倍率
	cacheDefaultRetention  int32   = 180  // 缓存保留时间（分钟）
	cacheCostRatio         float64 = 1.5  // 方案2 成本相对方案1 的最大允许倍数
)

// cacheModelConfig 读取当前模型（session.LastModelID）的缓存倍率与保留分钟，取不到回退默认。
func cacheModelConfig(session *structs.Chats) (float32, int32) {
	cfg := config.GlobalConfigSafe()
	if cfg == nil {
		return cacheDefaultMultiplier, cacheDefaultRetention
	}
	m, ok := cfg.Model.Models[int32(session.LastModelID)]
	if !ok {
		return cacheDefaultMultiplier, cacheDefaultRetention
	}
	mult := m.CachePriceMultiplier
	if mult <= 0 {
		mult = cacheDefaultMultiplier
	}
	ret := m.CacheRetentionMinutes
	if ret <= 0 {
		ret = cacheDefaultRetention
	}
	return mult, ret
}

// cacheTimeout 判断会话是否超过缓存保留时间（从最后活动时间 UpdatedAt 起算）。
func cacheTimeout(session *structs.Chats, retentionMinutes int32) bool {
	if retentionMinutes <= 0 || session.UpdatedAt.IsZero() {
		return false
	}
	return time.Since(session.UpdatedAt) > time.Duration(retentionMinutes)*time.Minute
}

// decideDiffPlan 对一个文件做「破坏缓存 vs 保留+diff」的硬性决策（不含软条件）。
// 返回 keep=true 表示「方案2 候选」，软条件成本比较由 insert 阶段的 KeepDiffPlan 复核。
// 以下情况一律走方案1（keep=false）：
//   - @temp/ 临时文件（内容在 ReferFiles，不做 diff）
//   - 超时（缓存已过期，保留无意义）
//   - 首次跟踪（oldContent 为空，无旧块可留）
//   - 内容无变化
//   - diff 总长度超过原文件（硬条件，强制破坏）
func decideDiffPlan(path, oldContent, newContent string, timeout bool, mult float32) (DiffPlan, bool) {
	if timeout || strings.HasPrefix(path, "@temp/") || oldContent == "" || oldContent == newContent {
		return DiffPlan{}, false
	}
	diff := u.UnifiedDiff(oldContent, newContent, path)
	if diff == "" {
		return DiffPlan{}, false
	}
	dTok := u.EstimateTokens(diff)
	oTok := u.EstimateTokens(oldContent)
	nTok := u.EstimateTokens(newContent)
	if dTok > oTok { // 硬条件：diff 比原文件还长
		return DiffPlan{}, false
	}
	oldBlock, ok := renderContentBlock(path, oldContent)
	if !ok {
		return DiffPlan{}, false
	}
	diffBlock := FileBlock{
		Name:   path,
		Size:   strconv.Itoa(len(diff)),
		Length: uint32(len(diff)),
		Text:   diff,
		Type:   "diff",
	}
	return DiffPlan{
		Keep:      true,
		OldBlock:  oldBlock,
		DiffBlock: diffBlock,
		oTok:      oTok,
		nTok:      nTok,
		dTok:      dTok,
		mult:      mult,
	}, true
}

// KeepDiffPlan 在 insert 阶段用真实 betweenTok（旧块锚点与 diff 块锚点之间的内容 token）复核软条件。
//   - 方案1（破坏缓存）：新内容块全价 + betweenTok 因前缀失效而全价重算
//   - 方案2（保留+diff）：旧块与 betweenTok 命中缓存（mult 倍）+ diff 块全价
//
// 返回 true 表示最终执行方案2；false 表示退化为方案1。
func KeepDiffPlan(plan DiffPlan, betweenTok int) bool {
	if !plan.Keep {
		return false
	}
	if betweenTok < 0 {
		betweenTok = 0
	}
	mult := plan.mult
	if mult <= 0 {
		mult = cacheDefaultMultiplier
	}
	cost1 := float64(plan.nTok + betweenTok)
	cost2 := float64(plan.oTok+betweenTok)*float64(mult) + float64(plan.dTok)
	return cost2 <= cacheCostRatio*cost1
}
