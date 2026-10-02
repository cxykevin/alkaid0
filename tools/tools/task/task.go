package task

import (
	_ "embed" // embed
	"strings"
	"text/template"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/edit"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
)

const toolName = "task"

//go:embed prompt.md
var prompt string

//go:embed task.md
var taskPrompt string

var taskTempate *template.Template

var logger = log.New("tools:task")

func init() {
	taskTempate = prompts.Load("tools:task:task", taskPrompt)
	// @task 作为虚拟对象接入 trace 的落位/差分机制（task → edit → trace 已有依赖，反向无环）
	trace.RegisterVirtualContent("@task", TaskContent)
}

// TaskContent 返回 @task 内容块，供 trace 层作为虚拟对象内容源使用。
// 内容块与普通 traced 文件一样按"事件跟随 / 末尾前移 / 差分"注入，不再进"历史之前的
// 全局注入块"：任务列表每轮都变，放在历史之前会让后面全部历史失去前缀缓存
// （docs/trace-cache-spec.md §10.1）。
func TaskContent(session *structs.Chats) (string, bool) {
	return renderTaskBlock(session), true
}

// renderTaskBlock 渲染 @task 任务列表内容块；空任务返回引导语（避免 ToolPrehookTemplate 渲染多余空行）。
func renderTaskBlock(session *structs.Chats) string {
	if session.Task == "" {
		return "Virtual object `@task` is empty. You can create a task plan by editing `@task` with the `edit` tool (`- [X] taskName: taskDetails`, 2-space indent per level)."
	}
	rendered, err := prompts.Render(taskTempate, session.Task)
	if err != nil {
		logger.Warn("task prompt render error: %v", err)
		return ""
	}
	return rendered
}

// buildPrompt 注入 @task 操作规范到 edit 工具 Description（edit PreHook，Priority 90）
func buildPrompt(session *structs.Chats) (string, error) {
	return prompt, nil
}

// updateInfo 向 cross 追加 PassInfo（与 tree 对齐，前端调用卡片仍由 edit.updateInfo 生成）
func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, _ string) (bool, []*any, error) {
	ret := any(edit.PassInfo{
		From:        "task",
		Description: "Task Manager",
		Parameters:  map[string]any{},
	})
	cross = append(cross, &ret)
	return true, cross, nil
}

// failResult 统一构造失败返回（pass=false 终止 edit 自身 writeFile）
func failResult(cross []*any, errMsg string) (bool, []*any, map[string]*any, error) {
	boolx := false
	success := any(boolx)
	errAny := any(errMsg)
	return false, cross, map[string]*any{
		"success": &success,
		"error":   &errAny,
	}, nil
}

// writeTask 拦截 edit 对 @task 的编辑（edit PostHook，Priority 110）
func writeTask(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	path, err := edit.CheckPath(mp)
	if err != nil {
		return true, cross, nil, nil // 路径异常 → 放行给 edit.writeFile
	}
	if path != "@task" {
		return true, cross, nil, nil // 非 @task → 放行
	}

	target, text, err := edit.CheckTargetText(mp)
	if err != nil {
		return failResult(cross, err.Error())
	}

	// 读取当前任务内容并应用编辑（session.Task 为空时按"新建文件"语义处理）
	content := session.Task
	newContent, err := edit.ProcessString(content, target, text, content != "")
	if err != nil {
		logger.Warn("task process error: %v", err)
		return failResult(cross, err.Error())
	}
	// 规范化首尾空白（ProcessString 会追加 \n）
	newContent = strings.TrimSpace(newContent)

	// 格式校验：失败返回 error 让 AI 根据错误信息修正后重试
	if _, err := ParseTask(newContent); err != nil {
		logger.Warn("task format invalid: %v", err)
		return failResult(cross, "task format error: "+err.Error())
	}

	// 持久化到 chats 表
	session.Task = newContent
	if err := session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("task", newContent).Error; err != nil {
		logger.Warn("task persist error: %v", err)
		return failResult(cross, "failed to persist task: "+err.Error())
	}

	// 构造 ACP plan entries 并推送到客户端（已通过 ParseTask，BuildPlanEntries 不会失败）
	if entries, err := BuildPlanEntries(newContent); err == nil {
		session.PushPlan(entries)
	}

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

func load() string {
	// edit 工具的 @task 拦截
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
			Func:     writeTask,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
