package task

import (
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

func TestParseTask_Valid(t *testing.T) {
	content := `- [-] 编写核心代码: 编写一个完整的核心代码实现功能
  - [X] 1.1 完成A模块: 完成A模块程序
  - [-] 1.2 完成B模块: 完成B模块程序
  - [ ] 1.3 完成C模块: 完成C模块程序

- [X] 编写测试: 编写单元测试`

	items, err := ParseTask(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 top-level items, got %d", len(items))
	}
	if items[0].TaskName != "编写核心代码" {
		t.Fatalf("expected name 编写核心代码, got %q", items[0].TaskName)
	}
	if items[0].Status != statusDoing {
		t.Fatalf("expected status -, got %q", items[0].Status)
	}
	if items[0].TaskDetail != "编写一个完整的核心代码实现功能" {
		t.Fatalf("unexpected detail %q", items[0].TaskDetail)
	}
	if len(items[0].Children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(items[0].Children))
	}
	ch := items[0].Children
	if ch[0].TaskName != "1.1 完成A模块" || ch[0].Status != statusDone {
		t.Fatalf("unexpected child[0]: %+v", ch[0])
	}
	if ch[1].Status != statusDoing || ch[2].Status != statusWaiting {
		t.Fatalf("unexpected child statuses")
	}
	if items[1].TaskName != "编写测试" || items[1].Status != statusDone {
		t.Fatalf("unexpected second item")
	}
}

func TestParseTask_EmptyContent(t *testing.T) {
	items, err := ParseTask("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	items, err = ParseTask("\n\n  \n")
	if err != nil {
		t.Fatalf("unexpected error for whitespace content: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestParseTask_CRLF(t *testing.T) {
	content := "- [ ] 任务A: 详情\r\n  - [X] 子任务: 子详情\r\n"
	items, err := ParseTask(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 || len(items[0].Children) != 1 {
		t.Fatalf("unexpected parse result: %+v", items)
	}
}

func TestParseTask_ColonInDetail(t *testing.T) {
	content := "- [ ] 任务A: 详情: 包含冒号"
	items, err := ParseTask(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0].TaskName != "任务A" {
		t.Fatalf("expected name 任务A, got %q", items[0].TaskName)
	}
	if items[0].TaskDetail != "详情: 包含冒号" {
		t.Fatalf("expected detail 详情: 包含冒号, got %q", items[0].TaskDetail)
	}
}

func TestParseTask_EmptyDetail(t *testing.T) {
	content := "- [ ] 仅名称:"
	items, err := ParseTask(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0].TaskName != "仅名称" || items[0].TaskDetail != "" {
		t.Fatalf("unexpected item: %+v", items[0])
	}
}

func TestParseTask_InvalidBullet(t *testing.T) {
	for _, c := range []string{
		"* [ ] 任务: 详情",
		"+ [ ] 任务: 详情",
		"1. [ ] 任务: 详情",
		"  * [ ] 任务: 详情",
	} {
		if _, err := ParseTask(c); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestParseTask_InvalidStatus(t *testing.T) {
	for _, c := range []string{
		"- [y] 任务: 详情",
		"- [x] 任务: 详情",
		"- [done] 任务: 详情",
		"- [] 任务: 详情",
	} {
		if _, err := ParseTask(c); err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestParseTask_InvalidIndent(t *testing.T) {
	// 3 空格缩进（非 2 的倍数）
	if _, err := ParseTask("- [ ] 任务: 详情\n   - [ ] 子任务: 详情"); err == nil {
		t.Fatalf("expected error for odd indent")
	}
	// 层级跳级：顶层后直接 4 空格缩进
	if _, err := ParseTask("- [ ] 任务: 详情\n    - [ ] 子任务: 详情"); err == nil {
		t.Fatalf("expected error for skipped level")
	}
	// tab 缩进
	if _, err := ParseTask("- [ ] 任务: 详情\n\t- [ ] 子任务: 详情"); err == nil {
		t.Fatalf("expected error for tab indent")
	}
}

func TestParseTask_MissingColon(t *testing.T) {
	if _, err := ParseTask("- [ ] 没有冒号"); err == nil {
		t.Fatalf("expected error for missing colon")
	}
}

func TestParseTask_EmptyName(t *testing.T) {
	if _, err := ParseTask("- [ ] : 详情"); err == nil {
		t.Fatalf("expected error for empty name")
	}
}

func TestParseTask_SiblingAfterDeeper(t *testing.T) {
	// 子任务后回到顶层兄弟合法
	content := "- [ ] 任务A: 详情\n  - [ ] 子任务: 详情\n- [X] 任务B: 详情"
	items, err := ParseTask(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 || len(items[0].Children) != 1 {
		t.Fatalf("unexpected parse result")
	}
}

func TestBuildPlanEntries(t *testing.T) {
	content := `- [-] 编写核心代码: 编写核心代码实现功能
  - [X] 1.1 完成A模块: 完成A模块程序
  - [ ] 1.2 完成B模块: 完成B模块程序
- [X] 编写测试: 编写单元测试`

	entries, err := BuildPlanEntries(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	want := []structs.PlanEntry{
		{Content: "编写核心代码", Priority: "medium", Status: "in_progress"},
		{Content: "  1.1 完成A模块", Priority: "medium", Status: "completed"},
		{Content: "  1.2 完成B模块", Priority: "medium", Status: "pending"},
		{Content: "编写测试", Priority: "medium", Status: "completed"},
	}
	for i, w := range want {
		e := entries[i]
		if e.Content != w.Content || e.Priority != w.Priority || e.Status != w.Status {
			t.Fatalf("entry[%d] = %+v, want %+v", i, e, w)
		}
	}
}

func TestBuildPlanEntries_Empty(t *testing.T) {
	entries, err := BuildPlanEntries("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}
