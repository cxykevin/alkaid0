package actions

import "github.com/cxykevin/alkaid0/ui/loop"

// ReasonMap 停止原因到字符串的映射
var ReasonMap = map[loop.StopReason]string{
	loop.StopReasonNone:        "end_turn",
	loop.StopReasonModel:       "end_turn",
	loop.StopReasonUser:        "cancelled",
	loop.StopReasonError:       "refusal",
	loop.StopReasonPendingTool: "end_turn",
}

// ToolNameToTypeMap 工具名称到类型的映射，用于规范化工具调用类型
var ToolNameToTypeMap = map[string]string{
	"agent":            "other",
	"scope":            "other",
	"activate_agent":   "other",
	"deactivate_agent": "other",
	"edit":             "edit",
	"read":             "read",
	"trace":            "read", // 兼容改名前（trace）落库的历史工具调用
	"run":              "execute",
}
