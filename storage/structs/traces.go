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
	// ReadMode 上一次注入该 path 时使用的受限视图（"light" = 大纲/截断摘要）。
	// 空串 = 全量。大文件首读注入受限视图；模型再次 read 同一 path 时升级为全量并清空本列。
	ReadMode string `gorm:"type:text"`
	// AnchorMsgID 上次注入该 path 内容块时，块所紧跟的可渲染消息 id（0 = 未注入）。
	// 用途：
	//  1. 内容未变化时让块留在原位（前缀命中缓存），而不是每轮按事件重新落位；
	//  2. 与最新事件 MsgID 比较，判定"变化是否来自新消息"——不来自新消息（后台刷新 / 外部改写）时
	//     把块前移到消息列表末尾，使每轮只重算尾巴。
	// 语义见 docs/trace-cache-spec.md §4.2。
	AnchorMsgID uint64 `gorm:"default:0"`

	// ---- 图片 trace（MediaKind="image"）----
	//
	// 图片与文本共用同一套事件/锚点/预算逻辑，但内容不可 diff，永远按完整块注入。
	// 只保存模型实际看到的那张（已按 VisionMaxImageDimension 缩放）：
	//   - MediaPath         .alkaid0/images/{sha256(缩放图)}.{ext}
	//   - MediaHash         缩放图字节的 sha256（上次实际注入的基线）
	//   - MediaSourceHash   源字节的 sha256（工作区文件 / @image store 的原始输入），用于变更检测
	//   - MediaMaxDim       生成缩放图时的配置，配置变化后工作区图片会重新生成
	// 物理路径只在服务端内部使用，AI 与客户端只看到 @image/{base36 id}。
	MediaKind       string `gorm:"type:text"`
	MediaPath       string `gorm:"type:text"`
	MediaSourceHash string `gorm:"type:text"`
	MediaHash       string `gorm:"type:text"`
	MediaMIME       string `gorm:"type:text"`
	MediaWidth      int
	MediaHeight     int
	MediaBytes      int
	MediaTokens     int
	MediaMaxDim     int

	// 设计注记：本表与会话之间有意不建立外键/关联——deleteChat 只删会话表、保留聊天
	// 记录（见 ui/funcs.DeleteChat），子表数据既不应阻止会话行删除、也不应被级联删除。
	// 历史库遗留的 fk_traces_chats 外键由 storage/migrate.MigrateRemoveChatForeignKeys
	// 一次性移除；全新库不再生成。
}
