package structs

// TraceEvent 记录某 path（文件或 @task）最近一次 read/edit 事件的位置信息。
// 由 provider/request/build.DetectTraceEvents 在构建请求体时从消息历史中检测得到，
// 供 trace 内容块"事件跟随"插入（不破坏前缀缓存）使用。
type TraceEvent struct {
	MsgID      uint64 // 事件所在 assistant 消息的 DB id
	ToolCallID string // 原生模式下工具调用 id（用于定位 role:tool 结果消息）
	IsEdit     bool   // true=edit，false=read
	IsTask     bool   // path == "@task"
	InRecent   bool   // 是否位于最近 5 轮完整回放范围内
}

// session.TemporyDataOfSession 临时键（跨 build 链路传递事件信息与内容块）。
const (
	// TempKeyTraceEvents 有事件的 path → 最近一次 read/edit 事件（map[string]*TraceEvent）。
	TempKeyTraceEvents = "trace:events"
	// TempKeyTraceFileBlocks 有事件的 traced 文件 → 渲染好的内容块（map[string]trace.FileBlock）。
	TempKeyTraceFileBlocks = "trace:fileblocks"
	// TempKeyTaskEventBlock @task 有最近 edit 事件时的任务列表内容块（string）。
	TempKeyTaskEventBlock = "task:eventblock"
)
