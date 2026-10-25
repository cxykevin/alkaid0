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
	cacheDefaultMultiplier float32 = 0.2 // 缓存命中 token 相对输入价格的倍率
	cacheDefaultRetention  int32   = 180 // 缓存保留时间（分钟）
	cacheCostRatio         float64 = 1.5 // 方案2 成本相对方案1 的最大允许倍数
	// diffMaxTOverOldRatio diff 相对原内容的长度上限（方案2 候选的上下文膨胀安全阀）。
	// 超过即放弃保留；1× 太保守（见 decideDiffPlan 注释），2× 在"少重算前缀"与"控制上下文膨胀"之间取平衡。
	diffMaxTOverOldRatio = 2.0
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

// lastRequestTime 返回会话「上一次请求」的时间，作为前缀缓存是否还有效的判断依据。
//
// 取最后一条已落库消息的 time 列（autoCreateTime，unix 秒）：消息是请求的产物，
// 它的落库时间就是上一轮请求的下界。同一轮内的多次工具循环会不断刷新这个值，
// 活跃回合因此永远不会被判超时；而隔了几小时再聊时，它正好停在最后一次请求上。
//
// 不能用 session.UpdatedAt：那是会话行的 autoUpdateTime，任何单列 UPDATE（如 SaveState
// 写 state、SetTraceAnchor 之外的 trace_id 更新）都会顺带刷新它，time.Since 恒≈0，
// 过期判断永远不成立（跨过保留期仍按"旧块还能命中"走方案2）。
func lastRequestTime(session *structs.Chats) time.Time {
	if session == nil || session.DB == nil || session.ID == 0 {
		return time.Time{}
	}
	var last structs.Messages
	if err := session.DB.Select("time").Where("chat_id = ?", session.ID).
		Order("id DESC").Limit(1).Find(&last).Error; err != nil {
		logger.Warn("query last message time failed: %v", err)
		return time.Time{}
	}
	if last.Time == 0 {
		return time.Time{}
	}
	return time.Unix(int64(last.Time), 0)
}

// cacheTimeout 判断距上一次请求是否已超过模型的缓存保留时间。
//
// 超过保留期说明供应商侧的前缀缓存大概率已被回收：此时"保留旧块 + diff"换不到任何
// 缓存折扣，只剩多传一份旧块的成本，应直接走方案1（完整块落到最新事件）。
// lastRequest 为零值（新会话、查询失败）时不判超时——保守按"缓存仍有效"处理。
func cacheTimeout(lastRequest time.Time, retentionMinutes int32) bool {
	if retentionMinutes <= 0 || lastRequest.IsZero() {
		return false
	}
	return time.Since(lastRequest) > time.Duration(retentionMinutes)*time.Minute
}

// AnchorMode 内容块落位方式（见 docs/trace-cache-spec.md §4.2）。
type AnchorMode int

const (
	// AnchorFull 完整内容块紧跟 MsgID 所指消息（最新事件或原注入锚点）。
	AnchorFull AnchorMode = iota
	// AnchorDiff 差分：旧块锚 PrevMsgID、diff 块锚 MsgID。
	AnchorDiff
	// AnchorDiffTail 差分（无新事件承载）：旧块锚 PrevMsgID、diff 块追加到消息列表末尾。
	// 用于虚拟对象（@tree）与后台刷新类内容：旧块字节稳定留在原锚点（命中缓存），
	// 增量块放在末尾，避免把整个对话前缀重算一遍。
	AnchorDiffTail
	// AnchorTail 完整内容块追加到消息列表末尾（内容变了但没有新消息承载，如后台刷新/外部改写）。
	AnchorTail
)

// AnchorPlan 单个 traced 文件本轮内容块的落位决策（跨 build 链路传递，存于 TempKeyTraceAnchorPlan）。
// 它与 DiffPlan 的分工：DiffPlan 是"破坏缓存 vs 保留+diff"的成本结论并携带旧块/diff 块；
// AnchorPlan 只描述"块插到哪"，由 build 层按消息链表解析成具体插入点。
type AnchorPlan struct {
	Mode      AnchorMode
	MsgID     uint64 // AnchorFull/AnchorDiff：最新事件或注入锚点所在消息 id
	PrevMsgID uint64 // AnchorDiff：最早事件（旧块）所在消息 id
	// Full 标记本轮该 path 实际注入了完整内容（AnchorFull / AnchorTail），
	// 供 build 层决定是否省略被完整内容覆盖的历史 edit 调用。
	Full bool
}

// decideDiffPlan 对一个文件做「破坏缓存 vs 保留+diff」的硬性决策（不含软条件）。
// 返回 keep=true 表示「方案2 候选」，软条件成本比较由 insert 阶段的 KeepDiffPlan 复核。
// 以下情况一律走方案1（keep=false）：
//   - @docs/ 只读文档快照（不可变，不做 diff）
//   - 超时（缓存已过期，保留无意义）
//   - 首次跟踪（oldContent 为空，无旧块可留）
//   - 内容无变化
//   - diff 总长度超过原内容的 diffMaxTOverOldRatio 倍（上下文膨胀安全阀）
//
// @temp/* 临时对象与普通文件同等对待（自 2026-09-26 起，见 docs/trace-cache-spec.md）：
// 它的 oldContent 同样来自 traces.last_content，只是内容源在 ReferFiles 而非磁盘。
func decideDiffPlan(path, oldContent, newContent string, timeout bool, mult float32) (DiffPlan, bool) {
	// 历史会话（@history/{session_id}）的滚动窗口：增量只保留新增消息，
	// 滚出窗口的旧消息不写成删除行。
	if structs.IsHistoryPath(path) {
		return decideHistoryDiffPlan(path, oldContent, newContent, timeout, mult)
	}
	if timeout || strings.HasPrefix(path, "@docs/") || oldContent == "" || oldContent == newContent {
		return DiffPlan{}, false
	}
	diff := u.UnifiedDiff(oldContent, newContent, path)
	if diff == "" {
		return DiffPlan{}, false
	}
	dTok := u.EstimateTokens(diff)
	oTok := u.EstimateTokens(oldContent)
	nTok := u.EstimateTokens(newContent)
	// 上下文膨胀安全阀：diff 超过原内容 diffMaxTOverOldRatio 倍时放弃保留。
	// 1×（diff 一超过原文件就放弃）对"旧块锚在很早位置"的对象过于激进：放弃 diff 意味着
	// 从旧锚点起重算整段历史，代价远大于多留一份缓存旧块 + 大 diff（实测：工作区树翻倍时
	// 那一轮命中率从 ~95% 掉到 51%）。当前取 2×，阈值以内的取舍交给 KeepDiffPlan 成本模型
	// （它已经把 betweenTok 的连锁成本算进去）。
	if float64(dTok) > float64(oTok)*diffMaxTOverOldRatio {
		return DiffPlan{}, false
	}
	// 旧块必须与"上次以完整块注入的字节"完全一致，否则前缀缓存恰在旧块处断裂。
	// 用 renderTraceFileFragment 而非 renderContentBlock：虚拟对象（@tree）的内容已自带行号，
	// 不能被二次编号（那会让旧块字节与上一轮注入的不同）。
	oldBlock, ok := renderTraceFileFragment(path, oldContent)
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

// KeepDiffPlan 在 insert 阶段用真实 suffixTok 复核软条件。
//
// suffixTok 是「旧块锚点之后、到消息列表末尾」的全部 token 总量：前缀一旦在旧块锚点处断裂，
// 这段内容整体按未命中重新计费。早期实现只统计旧锚点与 diff 锚点之间的消息正文，既漏掉
// 已注入的内容块（不在消息链表里）、漏掉 assistant 的 tool_calls 参数，也漏掉 diff 锚点之后
// 仍在请求里的尾巴——实测只能看到真实损失的 4%~13%，于是「搬家」的代价被低估一个数量级。
//
//   - 方案1（搬家）：旧位置那份块消失，旧锚点之后的整段都要全价重算 → suffixTok + nTok
//   - 方案2（保留+diff）：旧块与整段都留在原位命中缓存（mult），只多付 diff → suffixTok*mult + dTok
//
// 返回 true 表示最终执行方案2；false 表示退化为方案1。
func KeepDiffPlan(plan DiffPlan, suffixTok int) bool {
	if !plan.Keep {
		return false
	}
	if suffixTok < 0 {
		suffixTok = 0
	}
	mult := plan.mult
	if mult <= 0 {
		mult = cacheDefaultMultiplier
	}
	cost1 := float64(suffixTok + plan.nTok)
	cost2 := float64(suffixTok)*float64(mult) + float64(plan.dTok)
	return cost2 <= cacheCostRatio*cost1
}
