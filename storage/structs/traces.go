package structs

// Traces 文件跟踪表
type Traces struct {
	Path    string `gorm:"primaryKey"`
	ChatID  uint32 `gorm:"primaryKey"`
	AgentID string `gorm:"primaryKey"`
	TraceID uint64
	// LastContent 方案2 diff 的「旧端存档」= 上次以完整块注入上下文的文件原始内容（空 = 首次跟踪）。
	// 只在方案1（注入完整块）时推进；方案2（旧块+diff）时不推进，保证下次 diff 旧端稳定、
	// 旧块字节与上次注入一致，前缀缓存不被连续编辑破坏。
	// 对 @temp/* 临时对象同样维护：它同时是"上次注入的字节"，用于判定"内容变了但没有新事件"。
	LastContent string
	// AnchorMsgID 上次注入该 path 内容块时，块所紧跟的可渲染消息 id（0 = 未注入）。
	// 用途：
	//  1. 内容未变化时让块留在原位（前缀命中缓存），而不是每轮按事件重新落位；
	//  2. 与最新事件 MsgID 比较，判定"变化是否来自新消息"——不来自新消息（后台刷新 / 外部改写）时
	//     把块前移到消息列表末尾，使每轮只重算尾巴。
	// 语义见 docs/trace-cache-spec.md §4.2。
	AnchorMsgID uint64 `gorm:"default:0"`
	Chats       *Chats `gorm:"foreignKey:ChatID"`
}
