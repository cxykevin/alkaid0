package task

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// 任务状态（markdown 括号内的状态字符）
const (
	statusWaiting = " " // [ ] waiting
	statusDoing   = "-" // [-] doing
	statusDone    = "X" // [X] done
)

// TaskItem 解析后的任务条目
type TaskItem struct {
	Level      int         // 层级（0 为顶层）
	Status     string      // 原始状态字符：" " / "-" / "X"
	TaskName   string      // 冒号前的名称（TrimSpace 后）
	TaskDetail string      // 冒号后的详情（TrimSpace 后，可为空）
	Children   []*TaskItem // 子任务
}

// taskLineRe 行格式：可选空格缩进 + "- " + "[状态] " + 名称与详情。
// 只允许 "-" 项目符号，状态只允许 [ ] / [-] / [X]。
var taskLineRe = regexp.MustCompile(`^( *)- \[([ X-])\] (.*)$`)

// ParseTask 解析并校验任务 markdown。
// 规则：空行跳过；每行必须以 "- " 开头且为 `- [X] taskName: taskDetails`；
// 缩进必须是 2 空格的倍数且层级只能逐级递增；taskName 不能为空。
// 失败返回带行号的错误，供 AI 修正后重试。空内容返回空切片、nil error。
func ParseTask(content string) ([]*TaskItem, error) {
	lines := strings.Split(content, "\n")
	items := make([]*TaskItem, 0, len(lines))
	stack := make([]*TaskItem, 0, 8) // 父级链

	for idx, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue // 空行（含 \r 尾、纯空白）跳过
		}
		m := taskLineRe.FindStringSubmatch(raw)
		if m == nil {
			return nil, fmt.Errorf("line %d: 必须以 `- [ ]`/`[-]`/`[X]` 开头（仅允许 `-` 项目符号）：%q", idx+1, raw)
		}
		indent := len(m[1])
		if indent%2 != 0 {
			return nil, fmt.Errorf("line %d: 缩进必须是 2 空格的倍数（当前 %d 空格）：%q", idx+1, indent, raw)
		}
		level := indent / 2
		if len(stack) > 0 && level > stack[len(stack)-1].Level+1 {
			return nil, fmt.Errorf("line %d: 缩进跳级（当前层级 %d，最近父级层级 %d）：%q", idx+1, level, stack[len(stack)-1].Level, raw)
		}

		rest := m[3]
		// 用第一个 ":" 分割 taskName 与 taskDetails
		name, detail := rest, ""
		if ci := strings.Index(rest, ":"); ci >= 0 {
			name, detail = rest[:ci], rest[ci+1:]
		} else {
			return nil, fmt.Errorf("line %d: 缺少 `:` 分隔 taskName 与 taskDetails：%q", idx+1, raw)
		}
		name = strings.TrimSpace(name)
		detail = strings.TrimSpace(detail)
		if name == "" {
			return nil, fmt.Errorf("line %d: taskName 不能为空：%q", idx+1, raw)
		}

		item := &TaskItem{Level: level, Status: m[2], TaskName: name, TaskDetail: detail}

		// 维护父子链：弹出层级 >= 当前层级的祖先
		for len(stack) > 0 && stack[len(stack)-1].Level >= level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			stack[len(stack)-1].Children = append(stack[len(stack)-1].Children, item)
		} else {
			items = append(items, item)
		}
		stack = append(stack, item)
	}
	return items, nil
}

// acpStatus 映射 markdown 状态到 ACP plan 状态
func acpStatus(st string) string {
	switch st {
	case statusWaiting:
		return "pending"
	case statusDoing:
		return "in_progress"
	case statusDone:
		return "completed"
	}
	return "pending"
}

// BuildPlanEntries 从任务 markdown 构造 ACP plan entries。
// 每次推送完整列表，客户端整体替换。content 只展示 taskName（不含 details），
// 嵌套层级用 2 空格缩进保留在 content 中，客户端扁平列表也能看出父子关系。
func BuildPlanEntries(content string) ([]structs.PlanEntry, error) {
	items, err := ParseTask(content)
	if err != nil {
		return nil, err
	}
	entries := make([]structs.PlanEntry, 0, len(items))
	var walk func(level int, it *TaskItem)
	walk = func(level int, it *TaskItem) {
		name := it.TaskName
		if level > 0 {
			name = strings.Repeat("  ", level) + name
		}
		entries = append(entries, structs.PlanEntry{
			Content:  name,
			Priority: "medium",
			Status:   acpStatus(it.Status),
		})
		for _, c := range it.Children {
			walk(level+1, c)
		}
	}
	for _, it := range items {
		walk(0, it)
	}
	return entries, nil
}
