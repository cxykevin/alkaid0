package mask

import (
	"sync"

	configStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/library/ahocorasick"
	"gorm.io/gorm"
)

// ClientMasker 是发往客户端内容的脱敏视图（演示 / 直播用）。
//
// 它与请求侧的 Engine 共用 db 中的映射表，因此同一个原值在模型侧与客户端
// 显示为同一个假值；但它只做「原值 → 假值」方向，从不还原：
//   - Text 用于整段文本（用户消息、历史回放、工具参数/结果、摘要），
//     走与出站脱敏相同的正则检测，能发现上下文中从未出现过的敏感值；
//   - Stream 用于 AI 正文 / 思考的流式 chunk，按已知原值用 AC 自动机替换，
//     被切分到多个 chunk 的敏感值也能完整命中，不会漏出半截。
//
// 展示脱敏只影响发往客户端的内容：模型输入与本地数据库始终保留原文。
type ClientMasker struct {
	db *gorm.DB

	mu      sync.Mutex
	eng     *Engine
	cfg     configStructs.DataMaskConfig
	version uint64
	streams map[StreamKey]*ahocorasick.Replacer
}

// NewClientMasker 创建客户端展示脱敏器。
// db 为空、脱敏总开关关闭或映射表不存在时返回 nil。
func NewClientMasker(db *gorm.DB) *ClientMasker {
	eng := NewEngine(db)
	if eng == nil {
		return nil
	}
	return &ClientMasker{
		db:      db,
		eng:     eng,
		cfg:     eng.cfg,
		version: mappingVersion.Load(),
		streams: make(map[StreamKey]*ahocorasick.Replacer),
	}
}

// SetConfig 同步最新的脱敏配置，使 /reload 后不同掩码类型的开关立即生效。
func (cm *ClientMasker) SetConfig(cfg configStructs.DataMaskConfig) {
	if cm == nil {
		return
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.cfg = cfg
	if cm.eng != nil {
		// Engine.maskText 在 RLock 下读取 cfg；这里必须用写锁更新，
		// 否则 /reload 与正在进行的 Text 调用会构成 data race。
		cm.eng.mu.Lock()
		cm.eng.cfg = cfg
		cm.eng.mu.Unlock()
	}
}

// Text 返回整段文本的客户端展示内容（敏感值替换为假值）。
func (cm *ClientMasker) Text(text string) string {
	if cm == nil || text == "" {
		return text
	}
	cm.mu.Lock()
	eng := cm.engineLocked()
	cm.mu.Unlock()
	if eng == nil {
		return text
	}
	return eng.MaskText(text)
}

// Stream 把一个流式文本分片转换为客户端展示内容。
// 状态按 key 跨调用保持；流结束时必须调用 FinishStream 刷出残留。
func (cm *ClientMasker) Stream(key StreamKey, text string) string {
	if cm == nil || text == "" {
		return text
	}
	cm.mu.Lock()
	r := cm.streamLocked(key)
	cm.mu.Unlock()
	if r == nil {
		return text
	}
	return string(r.Stream([]byte(text)))
}

// FinishStream 刷出指定流被扣留的尾部（可能是某个敏感值的前缀），
// 并释放该流的状态。
func (cm *ClientMasker) FinishStream(key StreamKey) string {
	if cm == nil {
		return ""
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	r := cm.streams[key]
	if r == nil {
		return ""
	}
	delete(cm.streams, key)
	return string(r.Finish())
}

// engineLocked 返回内存映射与数据库同步的 Engine（调用方须持 cm.mu）。
//
// 请求侧每次请求都会新建 Engine 并可能写入新映射；ClientMasker 长期持有
// 内存映射，必须按 mappingVersion 重新加载，否则会把别的引擎生成的假值
// 当成新的敏感值再脱敏一遍（例如假手机号再被识别为手机号）。
func (cm *ClientMasker) engineLocked() *Engine {
	v := mappingVersion.Load()
	if cm.eng != nil && cm.version == v {
		return cm.eng
	}
	eng := NewEngine(cm.db)
	if eng == nil {
		cm.eng = nil
		return nil
	}
	// NewEngine 读取的是当前全局配置；以 SetConfig 收到的配置为准，
	// 避免热重载瞬间两处配置不一致。
	eng.cfg = cm.cfg
	cm.eng = eng
	cm.version = v
	return eng
}

// streamLocked 取出（必要时创建）指定流的脱敏替换器（调用方须持 cm.mu）。
func (cm *ClientMasker) streamLocked(key StreamKey) *ahocorasick.Replacer {
	if r, ok := cm.streams[key]; ok {
		return r
	}
	eng := cm.engineLocked()
	if eng == nil {
		return nil
	}
	r := ahocorasick.NewReplacer(eng.maskItems())
	cm.streams[key] = r
	return r
}
