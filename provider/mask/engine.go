package mask

import (
	"sync"

	"github.com/cxykevin/alkaid0/config"
	configStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/library/ahocorasick"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

var logger = log.New("mask")

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
	Kind   uint8 // StreamKindContent / StreamKindArguments
	Index  int   // 工具调用下标（Kind == StreamKindArguments 时有效）
}

const (
	// StreamKindContent 正文流
	StreamKindContent uint8 = iota
	// StreamKindArguments 工具调用参数流
	StreamKindArguments
)

// PendingArg 是流结束时某条工具参数流的残留字节。
type PendingArg struct {
	Choice int
	Index  int
	Text   string
}

// Engine 是安全 key 上下文的引擎：出站脱敏 + 响应流式还原。
// 每个请求创建一个 Engine（映射表很小，全量加载 + 重建 AC 自动机开销可忽略）。
type Engine struct {
	db           *gorm.DB
	cfg          configStructs.DataMaskConfig
	mu           sync.RWMutex
	origToMask   map[KeyRef]string // 原值 → 假值（脱敏方向）
	maskToOrig   map[string]string // 假值 → 原值（还原方向）
	reps         map[StreamKey]*ahocorasick.Replacer
	repItems     []ahocorasick.Item // 重建还原器用的条目（惰性创建各流时复用）
	repOrder     []StreamKey        // 流创建顺序，便于稳定地刷出残留
	reasoningRep *ahocorasick.Replacer
	custom       []string // /mask add 的自定义脱敏值（精确匹配）
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

// loadMappings 全量加载映射并重建两个还原器。
func (e *Engine) loadMappings() {
	var rows []storageStructs.KeyMapping
	if err := e.db.Find(&rows).Error; err != nil {
		logger.Error("data mask: load mappings: %v", err)
		return
	}
	for _, r := range rows {
		e.origToMask[KeyRef{r.KeyType, r.Original}] = r.Masked
		e.maskToOrig[r.Masked] = r.Original
	}
	e.rebuildReplacersLocked()
}

// rebuildReplacersLocked 依据当前 maskToOrig 重建还原器（调用方须持写锁）。
// 各条流按需惰性创建，重建时连同已有状态一起清空。
func (e *Engine) rebuildReplacersLocked() {
	items := make([]ahocorasick.Item, 0, len(e.maskToOrig))
	for masked, orig := range e.maskToOrig {
		items = append(items, ahocorasick.Item{Keyword: masked, Replace: orig})
	}
	e.repItems = items
	e.reps = make(map[StreamKey]*ahocorasick.Replacer)
	e.repOrder = nil
	e.reasoningRep = ahocorasick.NewReplacer(items)
}

// replacerLocked 取出（必要时创建）指定流的还原器。调用方须持写锁。
func (e *Engine) replacerLocked(key StreamKey) *ahocorasick.Replacer {
	if e.reps == nil {
		e.reps = make(map[StreamKey]*ahocorasick.Replacer)
	}
	if r, ok := e.reps[key]; ok {
		return r
	}
	r := ahocorasick.NewReplacer(e.repItems)
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
	// 新映射入库后重建还原器，保证本次请求响应的还原覆盖新假值
	if changed {
		e.mu.Lock()
		e.rebuildReplacersLocked()
		e.mu.Unlock()
	}
	return out
}

// maskText 对单段文本执行脱敏替换（从右到左，假值等长索引不回移）。
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
	result := []byte(text)
	for i := len(spans) - 1; i >= 0; i-- {
		s := spans[i]
		fake, err := e.lookupOrCreate(s.Type, s.Original)
		if err != nil {
			continue
		}
		if fake == "" || fake == s.Original {
			continue
		}
		buf := make([]byte, 0, len(result))
		buf = append(buf, result[:s.Start]...)
		buf = append(buf, fake...)
		buf = append(buf, result[s.End:]...)
		result = buf
	}
	return string(result), true
}

// lookupOrCreate 查找或创建「原值 → 假值」映射（db 为唯一事实源，保证同 key 同假值）。
func (e *Engine) lookupOrCreate(typ, original string) (string, error) {
	e.mu.RLock()
	if m, ok := e.origToMask[KeyRef{typ, original}]; ok {
		e.mu.RUnlock()
		return m, nil
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	// double-check
	if m, ok := e.origToMask[KeyRef{typ, original}]; ok {
		return m, nil
	}

	for range 8 {
		fake := genFake(original, typ)
		if fake == original || fake == "" {
			continue
		}
		row := storageStructs.KeyMapping{KeyType: typ, Original: original, Masked: fake}
		if err := e.db.Create(&row).Error; err == nil {
			e.origToMask[KeyRef{typ, original}] = fake
			e.maskToOrig[fake] = original
			return fake, nil
		}
		// 唯一索引冲突：可能是并发请求已建同 original（复用），或 masked 冲突（换一个）
		var existing storageStructs.KeyMapping
		if e.db.Where("key_type = ? AND original = ?", typ, original).First(&existing).Error == nil {
			e.origToMask[KeyRef{typ, original}] = existing.Masked
			e.maskToOrig[existing.Masked] = existing.Original
			return existing.Masked, nil
		}
	}
	logger.Warn("data mask: failed to create mapping for %s", typ)
	return original, nil
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
	e.mu.RLock()
	r := e.reasoningRep
	e.mu.RUnlock()
	if r == nil || s == "" {
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
