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

// Engine 是安全 key 上下文的引擎：出站脱敏 + 响应流式还原。
// 每个请求创建一个 Engine（映射表很小，全量加载 + 重建 AC 自动机开销可忽略）。
type Engine struct {
	db           *gorm.DB
	cfg          configStructs.DataMaskConfig
	mu           sync.RWMutex
	origToMask   map[KeyRef]string     // 原值 → 假值（脱敏方向）
	maskToOrig   map[string]string     // 假值 → 原值（还原方向）
	contentRep   *ahocorasick.Replacer // 正文流还原器
	reasoningRep *ahocorasick.Replacer // 思考流还原器（独立状态，避免跨流误拼接）
	custom       []string              // /mask add 的自定义脱敏值（精确匹配）
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
func (e *Engine) rebuildReplacersLocked() {
	items := make([]ahocorasick.Item, 0, len(e.maskToOrig))
	for masked, orig := range e.maskToOrig {
		items = append(items, ahocorasick.Item{Keyword: masked, Replace: orig})
	}
	e.contentRep = ahocorasick.NewReplacer(items)
	e.reasoningRep = ahocorasick.NewReplacer(items)
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
		if rc := out[i].ReasoningContent; rc != nil && *rc != "" {
			if nr, ok := e.maskText(*rc); ok {
				out[i].ReasoningContent = &nr
				changed = true
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

// RestoreContent 流式还原正文 chunk（状态跨调用保持）。
func (e *Engine) RestoreContent(s string) string {
	e.mu.RLock()
	r := e.contentRep
	e.mu.RUnlock()
	if r == nil || s == "" {
		return s
	}
	return string(r.Stream([]byte(s)))
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

// FinishRestore 流结束时刷出两条流的残留缓冲。
func (e *Engine) FinishRestore() (content, reasoning string) {
	e.mu.RLock()
	c, r := e.contentRep, e.reasoningRep
	e.mu.RUnlock()
	if c != nil {
		content = string(c.Finish())
	}
	if r != nil {
		reasoning = string(r.Finish())
	}
	return content, reasoning
}
