package task

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/tools/edit"
)

func strPtr(s string) *any {
	a := any(s)
	return &a
}

func newTestSession(t *testing.T) *structs.Chats {
	t.Helper()
	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	chat := &structs.Chats{}
	if err := db.Create(chat).Error; err != nil {
		t.Fatal(err)
	}
	return &structs.Chats{
		ID: chat.ID,
		DB: db,
	}
}

// TestWriteTask_PathNotTask 测试路径不是@task时放行
func TestWriteTask_PathNotTask(t *testing.T) {
	session := &structs.Chats{}
	mp := map[string]*any{
		"path": strPtr("some_file.txt"),
	}
	success, _, resultMap, _ := writeTask(session, mp, []*any{})
	if !success {
		t.Fatalf("expected pass=true for non-@task path")
	}
	if resultMap != nil {
		t.Fatalf("expected nil result map for non-@task path")
	}
}

// TestWriteTask_MissingPath 测试缺少path参数时放行
func TestWriteTask_MissingPath(t *testing.T) {
	session := &structs.Chats{}
	success, _, resultMap, _ := writeTask(session, map[string]*any{}, []*any{})
	if !success {
		t.Fatalf("expected pass=true on path error")
	}
	if resultMap != nil {
		t.Fatalf("expected nil result map on path error")
	}
}

// TestWriteTask_MissingTargetText 测试缺少target和text参数
func TestWriteTask_MissingTargetText(t *testing.T) {
	session := &structs.Chats{}
	mp := map[string]*any{
		"path": strPtr("@task"),
	}
	success, _, resultMap, _ := writeTask(session, mp, []*any{})
	if success {
		t.Fatalf("expected pass=false when missing target/text")
	}
	if resultMap == nil {
		t.Fatalf("expected result map with error")
	}
	if successPtr, ok := resultMap["success"]; ok {
		if successBool, ok := (*successPtr).(bool); !ok || successBool {
			t.Fatalf("expected success=false in result")
		}
	}
	if _, ok := resultMap["error"]; !ok {
		t.Fatalf("expected error in result map")
	}
}

// TestWriteTask_ValidEdit 测试合法编辑：更新内存、落库、触发 ACP plan 推送
func TestWriteTask_ValidEdit(t *testing.T) {
	session := newTestSession(t)
	var captured []structs.PlanEntry
	session.SetPlanPushFn(func(entries []structs.PlanEntry) {
		captured = entries
	})

	mp := map[string]*any{
		"path":   strPtr("@task"),
		"target": strPtr(""),
		"text":   strPtr("- [X] 编写核心代码: 编写核心代码实现功能"),
	}
	pass, _, resultMap, _ := writeTask(session, mp, []*any{})
	if pass {
		t.Fatalf("expected pass=false for @task")
	}
	if resultMap == nil {
		t.Fatalf("expected result map")
	}
	if successPtr, ok := resultMap["success"]; !ok {
		t.Fatalf("expected success in result")
	} else if successBool, ok := (*successPtr).(bool); !ok || !successBool {
		t.Fatalf("expected success=true in result")
	}

	want := "- [X] 编写核心代码: 编写核心代码实现功能"
	if session.Task != want {
		t.Fatalf("session.Task = %q, want %q", session.Task, want)
	}
	// DB 落库
	var persisted structs.Chats
	if err := session.DB.First(&persisted, session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Task != want {
		t.Fatalf("db task = %q, want %q", persisted.Task, want)
	}
	// ACP plan 推送
	if len(captured) != 1 {
		t.Fatalf("expected 1 plan entry, got %d", len(captured))
	}
	if captured[0].Content != "编写核心代码" || captured[0].Status != "completed" || captured[0].Priority != "medium" {
		t.Fatalf("unexpected plan entry: %+v", captured[0])
	}
}

// TestWriteTask_InvalidFormat 测试格式错误：返回 error 且不落库
func TestWriteTask_InvalidFormat(t *testing.T) {
	session := newTestSession(t)
	session.Task = "- [ ] 已有任务: 详情"
	if err := session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("task", session.Task).Error; err != nil {
		t.Fatal(err)
	}

	// 用一个会破坏格式的编辑：追加非法行
	mp := map[string]*any{
		"path":   strPtr("@task"),
		"target": strPtr(""),
		"text":   strPtr("这是一个非法的任务行"),
	}
	pass, _, resultMap, _ := writeTask(session, mp, []*any{})
	if pass {
		t.Fatalf("expected pass=false on invalid format")
	}
	if successPtr, ok := resultMap["success"]; ok {
		if successBool, ok := (*successPtr).(bool); !ok || successBool {
			t.Fatalf("expected success=false on invalid format")
		}
	}
	if _, ok := resultMap["error"]; !ok {
		t.Fatalf("expected error in result map")
	}
	// session.Task 不应被修改
	if session.Task != "- [ ] 已有任务: 详情" {
		t.Fatalf("session.Task should not change, got %q", session.Task)
	}
	// DB 不应被修改
	var persisted structs.Chats
	if err := session.DB.First(&persisted, session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Task != "- [ ] 已有任务: 详情" {
		t.Fatalf("db task should not change, got %q", persisted.Task)
	}
}

// TestWriteTask_ClearTask 测试清空任务
func TestWriteTask_ClearTask(t *testing.T) {
	session := newTestSession(t)
	session.Task = "- [X] 任务: 详情"
	if err := session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("task", session.Task).Error; err != nil {
		t.Fatal(err)
	}
	var captured []structs.PlanEntry
	session.SetPlanPushFn(func(entries []structs.PlanEntry) {
		captured = entries
	})

	mp := map[string]*any{
		"path":   strPtr("@task"),
		"target": strPtr("@all"),
		"text":   strPtr(""),
	}
	pass, _, resultMap, _ := writeTask(session, mp, []*any{})
	if pass {
		t.Fatalf("expected pass=false for @task")
	}
	if successPtr, ok := resultMap["success"]; !ok {
		t.Fatalf("expected success in result")
	} else if successBool, ok := (*successPtr).(bool); !ok || !successBool {
		t.Fatalf("expected success=true in result")
	}
	if session.Task != "" {
		t.Fatalf("expected empty task, got %q", session.Task)
	}
	if len(captured) != 0 {
		t.Fatalf("expected 0 plan entries after clear, got %d", len(captured))
	}
}

// TestBuildGlobalPrompt_Empty 测试空任务时返回提示而非空串
func TestBuildGlobalPrompt_Empty(t *testing.T) {
	session := &structs.Chats{}
	out, err := buildGlobalPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatalf("expected non-empty prompt for empty task")
	}
}

// TestBuildGlobalPrompt_NonEmpty 测试非空任务时渲染 markdown
func TestBuildGlobalPrompt_NonEmpty(t *testing.T) {
	session := &structs.Chats{Task: "- [X] 任务: 详情"}
	out, err := buildGlobalPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatalf("expected non-empty output")
	}
	if !strings.Contains(out, "- [X] 任务: 详情") {
		t.Fatalf("expected task content in output, got %q", out)
	}
}

// TestUpdateInfo 测试 OnHook 向 cross 追加 PassInfo
func TestUpdateInfo(t *testing.T) {
	session := &structs.Chats{}
	cross := []*any{}
	pass, newCross, err := updateInfo(session, nil, cross, "tool_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !pass {
		t.Fatalf("expected pass=true")
	}
	if len(newCross) != 1 {
		t.Fatalf("expected 1 cross item, got %d", len(newCross))
	}
	pi, ok := (*newCross[0]).(edit.PassInfo)
	if !ok {
		t.Fatalf("expected PassInfo, got %T", *newCross[0])
	}
	if pi.From != "task" {
		t.Fatalf("expected From=task, got %q", pi.From)
	}
}

// TestBuildGlobalPrompt_WithTaskEvent @task 有最近 edit 事件时，顶部不放，事件块存 session。
func TestBuildGlobalPrompt_WithTaskEvent(t *testing.T) {
	session := &structs.Chats{
		Task: "- [X] 任务: 详情",
		TemporyDataOfSession: map[string]any{
			structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
				"@task": {MsgID: 1, ToolCallID: "call_1", IsEdit: true, IsTask: true, InRecent: true},
			},
		},
	}
	out, err := buildGlobalPrompt(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty top output when @task has event, got %q", out)
	}
	block, ok := session.TemporyDataOfSession[structs.TempKeyTaskEventBlock].(string)
	if !ok || block == "" {
		t.Fatalf("expected task event block in session")
	}
	if !strings.Contains(block, "- [X] 任务: 详情") {
		t.Fatalf("expected task content in event block, got %q", block)
	}
}
