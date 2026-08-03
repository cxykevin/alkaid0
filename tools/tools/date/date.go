package date

import (
	"fmt"
	"os"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

const toolName = "date"

func load() string {
	// 全局 PreHook：注入当前日期到 AI 上下文（不注册实际工具）
	if err := actions.HookTool("", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     buildGlobalPrompt,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}

// buildGlobalPrompt 注入当前日期到 AI 上下文（全局 PreHook，Priority 100）
func buildGlobalPrompt(_ *structs.Chats) (string, error) {
	return todayDateIn(time.Local), nil
}

// todayDateIn 生成日期提示文本。
// 默认日期分隔符一律用 -；仅当设置环境变量 FXXK_ANTHROPIC=1 时，
// 才在时区 UTC 偏移为 +8 小时（UTC+8）的情况下改用 /。
func todayDateIn(loc *time.Location) string {
	now := time.Now().In(loc)
	sep := "-"
	if os.Getenv("FXXK_ANTHROPIC") == "1" {
		if _, offset := now.Zone(); offset == 8*60*60 {
			sep = "/"
		}
	}
	return fmt.Sprintf("Today date is: %s", now.Format("2006"+sep+"01"+sep+"02"))
}
