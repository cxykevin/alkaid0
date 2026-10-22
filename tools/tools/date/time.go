package date

import (
	_ "embed" // embed
	"fmt"
	"time"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// timeToolName 查询当前本地时间的工具名。
const timeToolName = "time"

//go:embed time_prompt.md
var timePrompt string

// timeParas 空参数：该工具不接受任何参数（模型传 {} 即可）。
var timeParas = map[string]parser.ToolParameters{}

// loadTime 注册 time 工具。
func loadTime() string {
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            timeToolName,
		UserDescription: timePrompt,
		Parameters:      timeParas,
		ID:              timeToolName,
	})
	if err := actions.HookTool(timeToolName, &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		// OnHook 为 nil：ExecToolOnHook 的兜底逻辑会按统一 calling_info 格式补一份
		// 调用信息（无参数可展示），与其它工具的回放形状一致。
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     runTime,
		},
	}); err != nil {
		panic(err)
	}
	return timeToolName
}

// runTime 执行 time 工具：返回本机本地时间（含 UTC 偏移）。
//
// 结果形如 {"success": true, "time": "2026-10-22 22:14:56 GMT+8"}。
func runTime(_ *structs.Chats, _ map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	success := any(true)
	value := any(formatTimeIn(time.Now(), time.Local))
	return false, cross, map[string]*any{
		"success": &success,
		"time":    &value,
	}, nil
}

// FormatLocal 把 t 转成本地时区并格式化为 "2006-01-02 15:04:05 GMT±H[:MM]"。
//
// 供其它工具（cron 的 next_runs）复用同一套时间写法，避免两个工具对同一时刻
// 给出不同格式的字符串。
func FormatLocal(t time.Time) string {
	return formatTimeIn(t, time.Local)
}

// formatTimeIn 把 t 转成 loc 时区并格式化为 "2006-01-02 15:04:05 GMT±H[:MM]"。
//
// 偏移量写成 GMT±H 而不是时区缩写（CST 之类有歧义）；半小时/三刻钟时区带分钟段，
// 例如 GMT+5:30、GMT-3:30，整点时区省略分钟（GMT+8、GMT+0）。
func formatTimeIn(t time.Time, loc *time.Location) string {
	local := t.In(loc)
	_, offset := local.Zone()
	return local.Format("2006-01-02 15:04:05") + " " + formatGMTOffset(offset)
}

// formatGMTOffset 把秒级偏移格式化为 GMT±H[:MM]。
func formatGMTOffset(offset int) string {
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	hours := offset / 3600
	minutes := offset % 3600 / 60
	if minutes == 0 {
		return fmt.Sprintf("GMT%s%d", sign, hours)
	}
	return fmt.Sprintf("GMT%s%d:%02d", sign, hours, minutes)
}

func init() {
	index.AddIndex(loadTime)
}
