package goal

import (
	_ "embed" // embed
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/edit"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
)

const toolName = "goal"

//go:embed prompt.md
var prompt string

func init() {
	// @goal 作为只读虚拟对象：内容源供 read / file 读取；
	// 写入只能经 edit 钩子（仅未设置时可写一次），不会落成磁盘文件。
	trace.RegisterVirtualContent("@goal", GoalContent)
}

// GoalContent 返回 @goal 内容块。goal 未设置时 ok=false（read 会提示对象为空）。
func GoalContent(session *structs.Chats) (string, bool) {
	if session == nil {
		return "", false
	}
	goal := strings.TrimSpace(session.Goal)
	if goal == "" {
		return "", false
	}
	return "<goal>\n" + goal + "\n</goal>", true
}

// buildPrompt 注入 @goal 操作规范到 edit 工具 Description（edit PreHook，Priority 90）。
func buildPrompt(session *structs.Chats) (string, error) {
	return prompt, nil
}

// updateInfo 向 cross 追加 PassInfo（与 task/memory 对齐）。
func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, _ string) (bool, []*any, error) {
	ret := any(edit.PassInfo{
		From:        "goal",
		Description: "Session Goal",
		Parameters:  map[string]any{},
	})
	cross = append(cross, &ret)
	return true, cross, nil
}

// failResult 统一构造失败返回（pass=false 终止 edit 自身 writeFile）。
func failResult(cross []*any, errMsg string) (bool, []*any, map[string]*any, error) {
	boolx := false
	success := any(boolx)
	errAny := any(errMsg)
	return false, cross, map[string]*any{
		"success": &success,
		"error":   &errAny,
	}, nil
}

// writeGoal 拦截 edit 对 @goal 的编辑（edit PostHook，Priority 110）。
// 语义：仅在 goal 为空时允许写入一次；写入后不可变，只能由用户清除/覆盖。
func writeGoal(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	path, err := edit.CheckPath(mp)
	if err != nil {
		return true, cross, nil, nil // 路径异常 → 放行给 edit.writeFile
	}
	if path != "@goal" {
		return true, cross, nil, nil // 非 @goal → 放行
	}
	if session == nil || session.DB == nil {
		return failResult(cross, "goal is not available in this session")
	}
	if strings.TrimSpace(session.Goal) != "" {
		return failResult(cross, "goal is already set and immutable; only the user can replace or clear it with /goal")
	}

	target, text, err := edit.CheckTargetText(mp)
	if err != nil {
		return failResult(cross, err.Error())
	}

	// goal 是一个独立的短文本对象，不继承文件中任何既有内容。
	newContent, err := edit.ProcessString("", target, text, false)
	if err != nil {
		return failResult(cross, err.Error())
	}
	newContent = strings.TrimSpace(newContent)
	if newContent == "" {
		return failResult(cross, "goal must not be empty")
	}
	if n := len([]rune(newContent)); n > structs.MaxGoalRunes {
		return failResult(cross, fmt.Sprintf("goal is too long: %d runes (max %d)", n, structs.MaxGoalRunes))
	}

	if err := session.SaveGoal(newContent); err != nil {
		return failResult(cross, "failed to persist goal: "+err.Error())
	}
	session.PushGoal(newContent)

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

func load() string {
	// edit 工具的 @goal 拦截
	if err := actions.HookTool("edit", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 90,
			Func:     buildPrompt,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 110,
			Func:     updateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 110,
			Func:     writeGoal,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
