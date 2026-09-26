package structs

// TraceEvent 记录某 path（文件或 @task）最近一次 read/edit 事件的位置信息。
// 由 provider/request/build.DetectTraceEvents 在构建请求体时从消息历史中检测得到，
// 供 trace 内容块"事件跟随"插入（不破坏前缀缓存）使用。
type TraceEvent struct {
	MsgID      uint64 // 事件所在 assistant 消息的 DB id
	ToolCallID string // 原生模式下工具调用 id（用于定位 role:tool 结果消息）
	IsEdit     bool   // true=edit，false=read
	IsTask     bool   // path == "@task"
	// 保留字段：由 DetectTraceEvents 写入（是否位于最近 5 轮）；findEventAnchor 已不再消费——
	// 全部工具调用轮次完整回放后，锚定不再依赖该标记。
	InRecent bool
}

// session.TemporyDataOfSession 临时键（跨 build 链路传递事件信息与内容块）。
const (
	// TempKeyTraceEvents 有事件的 path → 最近一次 read/edit 事件（map[string]*TraceEvent）。
	TempKeyTraceEvents = "trace:events"
	// TempKeyTracePrevEvents 有事件的 path → 最早一次 read/edit 事件（map[string]*TraceEvent）。
	// 方案2（保留旧块+diff）时旧内容块的插入锚点。锚定最早事件使旧块位置固定、
	// 连续编辑不漂移，前缀缓存才能被保住。
	TempKeyTracePrevEvents = "trace:prevevents"
	// TempKeyTraceFileBlocks 有事件的 traced 文件 → 渲染好的内容块（map[string]trace.FileBlock）。
	TempKeyTraceFileBlocks = "trace:fileblocks"
	// TempKeyTraceDiffPlan 每个 traced 文件的缓存决策结果（map[string]trace.DiffPlan）。
	// 由 trace.RenderTraceBlocks 决策写入，供 build 包按方案拼装旧块/diff 块/新块。
	TempKeyTraceDiffPlan = "trace:diffplan"
	// TempKeyTraceAnchorPlan 每个 traced 文件本轮内容块的落位决策（map[string]*trace.AnchorPlan）。
	// 与 DiffPlan 的分工：DiffPlan 只描述"破坏缓存 vs 保留+diff"的成本结论并携带旧块/diff 块；
	// AnchorPlan 描述"块插到哪"（最新事件 / 原注入锚点 / 消息列表末尾 / 差分双锚点）。
	TempKeyTraceAnchorPlan = "trace:anchorplan"
	// TempKeyTraceConfirmedContent 保存当前会话中 Agent 最近一次确认/写入的内容，
	// 用于 edit 在写盘前区分 Agent 自身后续编辑与外部修改。
	TempKeyTraceConfirmedContent = "trace:confirmed_content"
	// TempKeyTraceDocsSnapshots 保存 @docs 文档的会话级 immutable snapshot。
	TempKeyTraceDocsSnapshots = "trace:docs_snapshots"
	// TempKeyTaskEventBlock @task 有最近 edit 事件时的任务列表内容块（string）。
	TempKeyTaskEventBlock = "task:eventblock"
	// TempKeySystemNotices 内部运行期通知（后台任务结束、shell 停止等）。
	// 它变化频繁且与对话无关，**不能**放进 system 消息——system 在 tools 之后，
	// 一次通知就会把 tools 之后的整个前缀（含全部历史）打掉（实测命中掉到 tools 前缀大小）。
	// 由 Build 写入、RequestBody 作为消息列表末尾的独立块注入。
	TempKeySystemNotices = "system:notices"
)
