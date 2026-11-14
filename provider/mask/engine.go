package mask

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cxykevin/alkaid0/config"
	configStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/library/ahocorasick"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

var logger = log.New("mask")

// mappingVersion 是进程内「原值 → 假值」映射表的变更代次。
//
// 每次新建映射（以及 /mask add、/mask del 改动）都会递增。请求侧 Engine 是
// 一次性的，但客户端展示侧的 ClientMasker 需要跨请求长期持有映射快照，
// 它据此判断内存里的映射是否落后于数据库，从而避免把已生成的假值
// 再次当成敏感值脱敏。
var mappingVersion atomic.Uint64

// KeyRef 按「类型 + 原值」唯一定位一个映射。
type KeyRef struct {
	Type     string
	Original string
}

// StreamKey 标识一条独立的流式字段。
//
// 为什么需要：还原器（Aho-Corasick）会把"可能是关键词前缀"的尾部字节扣留到下次调用
// 再输出。若正文与工具调用参数共用一个还原器状态，正文末尾被扣留的字节会被拼到
// 参数串前面（如 `s{"path"…`），导致工具调用 JSON 解析失败、整个回合中止；
// 多条工具参数流之间同样会互相串字节。因此每条流必须有独立状态。
type StreamKey struct {
	Choice int   // choices 下标
	Kind   uint8 // StreamKindContent / StreamKindArguments / StreamKindReasoning
	Index  int   // 工具调用下标（Kind == StreamKindArguments 时有效）
}

const (
	// StreamKindContent 正文流
	StreamKindContent uint8 = iota
	// StreamKindArguments 工具调用参数流
	StreamKindArguments
	// StreamKindReasoning 思考流（客户端展示脱敏用；与正文流状态隔离）
	StreamKindReasoning
)

// PendingArg 是流结束时某条工具参数流的残留字节。
type PendingArg struct {
	Choice int
	Index  int
	Text   string
}

// Engine 是安全 key 上下文的引擎：出站脱敏 + 响应流式还原。
//
// 每个请求创建一个 Engine：映射表全量加载进内存，AC 自动机按需构建一次并在该
// 请求的所有流之间共享（见 trieLocked）。自动机体积只与关键字总长度成正比，
// 映射表很大（长期会话累积的 key_mappings 可达数千行）时也不会随流数量放大。
type Engine struct {
	db            *gorm.DB
	cfg           configStructs.DataMaskConfig
	mu            sync.RWMutex
	origToMask    map[KeyRef]string // 原值 → 假值（脱敏方向）
	maskToOrig    map[string]string // 假值 → 原值（还原方向）
	origID        map[KeyRef]uint64 // 原值 → key_mappings 行 id（刷新 last_used 用）
	reps          map[StreamKey]*ahocorasick.Replacer
	repOrder      []StreamKey           // 流创建顺序，便于稳定地刷出残留
	reasoningRep  *ahocorasick.Replacer // 思考流的还原状态
	trie          *ahocorasick.Trie     // 所有流共享的自动机（惰性构建，见 trieLocked）
	trieVer       uint64                // trie 对应的 mappingVersion
	clientTrie    *ahocorasick.Trie     // 「原值 → 假值」方向的自动机（客户端展示脱敏）
	clientTrieVer uint64                // clientTrie 对应的 mappingVersion
	custom        []string              // /mask add 的自定义脱敏值（精确匹配）

	// usageMu 保护 touched：本次请求里命中的映射行 id（去重）。
	// 独立于 mu 是为了不让脱敏热路径（RLock）升级成写锁。
	usageMu sync.Mutex
	touched map[uint64]struct{}
}

// NewEngine 创建引擎。功能未启用 / db 为 nil / 映射表不存在时返回 nil（调用方零行为变化）。
func NewEngine(db *gorm.DB) *Engine {
	if db == nil {
		return nil
	}
	cfg := config.GlobalConfigSafe().DataMask
	if !cfg.Enable {
		return nil
	}
	if !db.Migrator().HasTable(&storageStructs.KeyMapping{}) {
		logger.Warn("data mask: key_mappings table not found, masking disabled")
		return nil
	}
	e := &Engine{
		db:         db,
		cfg:        cfg,
		origToMask: make(map[KeyRef]string),
		maskToOrig: make(map[string]string),
		origID:     make(map[KeyRef]uint64),
	}
	e.loadMappings()
	e.loadCustom()
	return e
}

// loadCustom 加载用户通过 /mask add 加入的自定义脱敏值。
func (e *Engine) loadCustom() {
	var rows []storageStructs.CustomMask
	if err := e.db.Find(&rows).Error; err != nil {
		logger.Warn("data mask: load custom masks: %v", err)
		return
	}
	for _, r := range rows {
		if r.Value != "" {
			e.custom = append(e.custom, r.Value)
		}
	}
}

// loadMappings 全量加载映射。自动机不在这里构建：见 trieLocked（惰性 + 共享）。
func (e *Engine) loadMappings() {
	var rows []storageStructs.KeyMapping
	if err := e.db.Find(&rows).Error; err != nil {
		logger.Error("data mask: load mappings: %v", err)
		return
	}
	for _, r := range rows {
		ref := KeyRef{r.KeyType, r.Original}
		e.origToMask[ref] = r.Masked
		e.maskToOrig[r.Masked] = r.Original
		// 记下行 id：命中时要批量刷新 last_used/hits（见 FlushUsage）。
		e.origID[ref] = r.ID
	}
}

// trieLocked 返回当前映射对应的自动机（调用方须持写锁）。
//
// 自动机是只读且跨流共享的：正文流、思考流与每条工具参数流各持一个轻量匹配状态
// （ahocorasick.Replacer），但只共用这一份 Trie。此前每条流都各自构建一份完整
// 自动机，而自动机体积随 key_mappings 行数线性增长（2255 条映射 ≈ 620 MiB），
// 一次请求里几条流就能把常驻内存推到数 GiB。
//
// 新映射建立时 mappingVersion 递增，这里按代次重建一次即可；已经建立的流继续用
// 旧自动机（它的未决缓冲不会被打断），新流用新自动机。
func (e *Engine) trieLocked() *ahocorasick.Trie {
	v := mappingVersion.Load()
	if e.trie != nil && e.trieVer == v {
		return e.trie
	}
	items := make([]ahocorasick.Item, 0, len(e.maskToOrig))
	for masked, orig := range e.maskToOrig {
		items = append(items, ahocorasick.Item{Keyword: masked, Replace: orig})
	}
	e.trie = ahocorasick.NewTrie(items)
	e.trieVer = v
	return e.trie
}

// invalidateTrieLocked 丢弃自动机缓存，下次还原时按最新映射重建（调用方须持写锁）。
func (e *Engine) invalidateTrieLocked() {
	e.trie = nil
}

// replacerLocked 取出（必要时创建）指定流的还原状态。调用方须持写锁。
func (e *Engine) replacerLocked(key StreamKey) *ahocorasick.Replacer {
	if e.reps == nil {
		e.reps = make(map[StreamKey]*ahocorasick.Replacer)
	}
	if r, ok := e.reps[key]; ok {
		return r
	}
	r := e.trieLocked().NewMatcher()
	e.reps[key] = r
	e.repOrder = append(e.repOrder, key)
	return r
}

// MaskMessages 对消息列表逐条脱敏。返回新切片；原切片不被修改。
func (e *Engine) MaskMessages(messages []structs.Message) []structs.Message {
	out := make([]structs.Message, len(messages))
	copy(out, messages)
	changed := false
	for i := range out {
		if out[i].Content != "" {
			if nc, ok := e.maskText(out[i].Content); ok {
				out[i].Content = nc
				changed = true
			}
		}
		if out[i].ReasoningContent != nil && *out[i].ReasoningContent != "" {
			if nr, ok := e.maskText(*out[i].ReasoningContent); ok {
				out[i].ReasoningContent = &nr
				changed = true
			}
		}
		for j := range out[i].ToolCalls {
			if out[i].ToolCalls[j].Function != nil && out[i].ToolCalls[j].Function.Arguments != "" {
				if na, ok := e.maskText(out[i].ToolCalls[j].Function.Arguments); ok {
					out[i].ToolCalls[j].Function.Arguments = na
					changed = true
				}
			}
		}
	}
	// 新映射入库后作废自动机缓存，保证本次请求响应的还原覆盖新假值（按需重建一次）。
	if changed {
		e.mu.Lock()
		e.invalidateTrieLocked()
		e.mu.Unlock()
	}
	// 请求级批量刷新 last_used/hits：淘汰只认这个字段，而整份 prompt 每轮都会
	// 重新脱敏，所以仍在发送窗口里的原值永远不会变"冷"。
	e.FlushUsage()
	return out
}

// MaskText 返回对单段文本脱敏后的内容（原值 → 同格式假值）。
// 与原文本等长替换，因此可以安全地用于保持格式的展示场景。
func (e *Engine) MaskText(text string) string {
	if e == nil || text == "" {
		return text
	}
	out, _ := e.maskText(text)
	return out
}

// maskTrie 返回「原值 → 假值」方向的自动机（客户端展示侧的流式脱敏使用）：
// 已知敏感值即使被切分到多个 chunk，也能被完整替换。
//
// 方向与还原用的 trie 相反（关键字是原值），因此单独缓存一份；同样只构建一次、
// 供该 ClientMasker 的所有流共享。
func (e *Engine) maskTrie() *ahocorasick.Trie {
	e.mu.Lock()
	defer e.mu.Unlock()
	v := mappingVersion.Load()
	if e.clientTrie != nil && e.clientTrieVer == v {
		return e.clientTrie
	}
	items := make([]ahocorasick.Item, 0, len(e.origToMask))
	for ref, fake := range e.origToMask {
		if ref.Original == "" || fake == "" || ref.Original == fake {
			continue
		}
		items = append(items, ahocorasick.Item{Keyword: ref.Original, Replace: fake})
	}
	e.clientTrie = ahocorasick.NewTrie(items)
	e.clientTrieVer = v
	return e.clientTrie
}

// maskText 对单段文本执行脱敏替换（单次前向拼接；假值与原值等长）。
func (e *Engine) maskText(text string) (string, bool) {
	e.mu.RLock()
	spans := detectSensitive(text, &e.cfg, e.maskToOrig)
	if len(e.custom) > 0 {
		spans = append(spans, detectCustom(text, e.custom)...)
		spans = resolveSpans(spans, e.maskToOrig)
	}
	e.mu.RUnlock()
	if len(spans) == 0 {
		return text, false
	}
	// 单次前向拼接：resolveSpans 已保证 spans 升序且互不重叠，假值与原值等长，
	// 因此一遍扫描即可完成。旧实现在循环里「删一段 + 重新拼整段」，K 个命中就要
	// K 份整段文本拷贝——长会话里单条消息可达数百 KB、命中几十处，白白放大
	// 内存峰值与 GC 压力（这条路径每次请求都会跑）。
	var b strings.Builder
	b.Grow(len(text) + 16)
	prev := 0
	replaced := false
	for _, s := range spans {
		fake, id, err := e.lookupOrCreate(s.Type, s.Original)
		if err != nil || fake == "" || fake == s.Original {
			continue
		}
		// 记录本次请求命中的映射：请求结束时批量刷新 last_used（淘汰的唯一依据）。
		e.markUsed(id)
		b.WriteString(text[prev:s.Start])
		b.WriteString(fake)
		prev = s.End
		replaced = true
	}
	if !replaced {
		return text, false
	}
	b.WriteString(text[prev:])
	return b.String(), true
}

// lookupOrCreate 查找或创建「原值 → 假值」映射（db 为唯一事实源，保证同 key 同假值）。
// 同时返回映射行的 id，供调用方记录"本次请求用到了它"（淘汰判断的唯一依据）。
func (e *Engine) lookupOrCreate(typ, original string) (string, uint64, error) {
	ref := KeyRef{typ, original}
	e.mu.RLock()
	if m, ok := e.origToMask[ref]; ok {
		id := e.origID[ref]
		e.mu.RUnlock()
		return m, id, nil
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	// double-check
	if m, ok := e.origToMask[ref]; ok {
		return m, e.origID[ref], nil
	}

	for range 8 {
		fake := genFake(original, typ)
		if fake == original || fake == "" {
			continue
		}
		// 创建即"热"：last_used 直接写当前时间，避免刚建的行被当成古董立刻淘汰。
		now := uint64(evictNow().Unix())
		row := storageStructs.KeyMapping{
			KeyType:  typ,
			Original: original,
			Masked:   fake,
			LastUsed: now,
			Hits:     1,
		}
		if err := e.db.Create(&row).Error; err == nil {
			e.origToMask[ref] = fake
			e.maskToOrig[fake] = original
			e.origID[ref] = row.ID
			mappingVersion.Add(1)
			return fake, row.ID, nil
		}
		// 唯一索引冲突：可能是并发请求已建同 original（复用），或 masked 冲突（换一个）
		var existing storageStructs.KeyMapping
		if e.db.Where("key_type = ? AND original = ?", typ, original).First(&existing).Error == nil {
			e.origToMask[ref] = existing.Masked
			e.maskToOrig[existing.Masked] = existing.Original
			if existing.LastUsed == 0 {
				// 历史行（新列尚未回填）：先当热行，别让第一次清理就删掉它。
				existing.LastUsed = now
				_ = e.db.Model(&storageStructs.KeyMapping{}).Where("id = ?", existing.ID).
					Update("last_used", now).Error
			}
			e.origID[ref] = existing.ID
			mappingVersion.Add(1)
			return existing.Masked, existing.ID, nil
		}
	}
	logger.Warn("data mask: failed to create mapping for %s", typ)
	return original, 0, nil
}

// markUsed 记录本次请求命中的映射行（去重），由 FlushUsage 批量落库。
func (e *Engine) markUsed(id uint64) {
	if e == nil || id == 0 {
		return
	}
	e.usageMu.Lock()
	if e.touched == nil {
		e.touched = make(map[uint64]struct{}, 8)
	}
	e.touched[id] = struct{}{}
	e.usageMu.Unlock()
}

// FlushUsage 把本次请求命中的映射批量写回 last_used / hits。
//
// 必须在请求真正发出前调用（MaskMessages 结尾）：淘汰只认 last_used，若延后到
// 响应结束才刷新，一个正在进行的请求所用的映射可能仍显示为"很久没用过"而被删掉。
// 每次请求只有一条 UPDATE，与消息条数无关。
func (e *Engine) FlushUsage() {
	if e == nil || e.db == nil {
		return
	}
	e.usageMu.Lock()
	ids := make([]uint64, 0, len(e.touched))
	for id := range e.touched {
		ids = append(ids, id)
	}
	clear(e.touched)
	e.usageMu.Unlock()
	if len(ids) == 0 {
		return
	}
	if err := e.db.Model(&storageStructs.KeyMapping{}).Where("id IN ?", ids).
		Updates(map[string]any{
			"last_used": uint64(evictNow().Unix()),
			"hits":      gorm.Expr("hits + 1"),
		}).Error; err != nil {
		// 刷新失败只影响淘汰判断（该行可能被过早淘汰），不影响本次脱敏。
		logger.Warn("data mask: refresh key mapping usage: %v", err)
	}
}

// RestoreStream 流式还原指定流的分片（状态仅在该流内跨调用保持）。
func (e *Engine) RestoreStream(key StreamKey, s string) string {
	if s == "" {
		return s
	}
	e.mu.Lock()
	r := e.replacerLocked(key)
	e.mu.Unlock()
	if r == nil {
		return s
	}
	return string(r.Stream([]byte(s)))
}

// RestoreContent 流式还原正文 chunk（等价于正文流的 RestoreStream，保留旧签名）。
func (e *Engine) RestoreContent(s string) string {
	return e.RestoreStream(StreamKey{Kind: StreamKindContent}, s)
}

// RestoreReasoning 流式还原思考 chunk（独立状态）。
func (e *Engine) RestoreReasoning(s string) string {
	if s == "" {
		return s
	}
	e.mu.Lock()
	if e.reasoningRep == nil {
		e.reasoningRep = e.trieLocked().NewMatcher()
	}
	r := e.reasoningRep
	e.mu.Unlock()
	if r == nil {
		return s
	}
	return string(r.Stream([]byte(s)))
}

// FinishRestore 流结束时刷出正文与思考流的残留缓冲（保留旧签名）。
func (e *Engine) FinishRestore() (content, reasoning string) {
	content, reasoning, _ = e.FinishAll()
	return content, reasoning
}

// FinishAll 流结束时刷出所有流的残留缓冲。
//
// 工具参数流的残留必须单独返回（PendingArg），由调用方按对应 tool_call 的
// arguments 增量下发 —— 绝不能像旧实现那样把参数残留混进正文，反之亦然。
func (e *Engine) FinishAll() (content, reasoning string, args []PendingArg) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, key := range e.repOrder {
		r := e.reps[key]
		if r == nil {
			continue
		}
		tail := string(r.Finish())
		if tail == "" {
			continue
		}
		switch key.Kind {
		case StreamKindArguments:
			args = append(args, PendingArg{Choice: key.Choice, Index: key.Index, Text: tail})
		default:
			content += tail
		}
	}
	if e.reasoningRep != nil {
		reasoning = string(e.reasoningRep.Finish())
	}
	return content, reasoning, args
}
