package run

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// workflowViewEvent 构造一条事件（Raw 与 Data 和真实解析结果一致）。
func workflowViewEvent(t *testing.T, raw string) WorkflowEvent {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("bad event json: %v", err)
	}
	typ, _ := data["type"].(string)
	return WorkflowEvent{Type: typ, Raw: json.RawMessage(raw), Data: data}
}

// TestWorkflowViewRender 验证视图格式：mermaid 边、节点状态勾选框、缩进日志与 Result，
// 节点顺序按 graph 中的键顺序。
func TestWorkflowViewRender(t *testing.T) {
	v := newWorkflowView()
	v.Apply(workflowViewEvent(t, `{"type":"graph","graph":{"nodes":{"scan":{"name":"Scan Docs"},"check":{"name":"Check"},"report":{"name":"Report"}},"edges":{"scan":["check","report"],"check":["report"]},"start":["scan"]}}`))
	v.Apply(workflowViewEvent(t, `{"type":"node","nodeId":"scan","state":"running"}`))
	v.Apply(workflowViewEvent(t, `{"type":"node_log","nodeId":"scan","message":"Scan Docs Start"}`))
	v.Apply(workflowViewEvent(t, `{"type":"node_log","nodeId":"scan","message":"Scan Docs Finished"}`))
	v.Apply(workflowViewEvent(t, `{"type":"node","nodeId":"scan","state":"done"}`))
	v.Apply(workflowViewEvent(t, `{"type":"node_result","nodeId":"scan","result":{"ok":true,"n":2}}`))
	v.Apply(workflowViewEvent(t, `{"type":"node","nodeId":"check","state":"error"}`))
	v.Apply(workflowViewEvent(t, `{"type":"node_result","nodeId":"check","result":"boom"}`))

	got := v.Render("")
	want := strings.Join([]string{
		"scan->check",
		"scan->report",
		"check->report",
		"- [X] scan: Scan Docs",
		"  Scan Docs Start",
		"  Scan Docs Finished",
		"  Result: {\"n\":2,\"ok\":true}",
		"- [X] check: Check",
		"  Result: boom",
		"- [ ] report: Report",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("render mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestWorkflowViewRenderFallsBackWithoutGraph 验证还没收到 graph 时 read 到的仍是
// 原始终端输出（与 terminal 一致，不在启动阶段显示空的节点图）。
func TestWorkflowViewRenderFallsBackWithoutGraph(t *testing.T) {
	v := newWorkflowView()
	if got := v.Render("raw output"); got != "raw output" {
		t.Fatalf("fallback = %q", got)
	}
}

// TestWorkflowViewRenderAppendsRawOutput 验证视图之后附加原始输出。
func TestWorkflowViewRenderAppendsRawOutput(t *testing.T) {
	v := newWorkflowView()
	v.Apply(workflowViewEvent(t, `{"type":"graph","graph":{"nodes":{"a":{"name":"A"}},"edges":{},"start":["a"]}}`))
	got := v.Render("line1\nline2")
	want := "- [ ] a: A\n\n" + workflowViewRawSeparator + "\nline1\nline2\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestWorkflowViewUsesNodeCodeNameFallback 兼容旧版 dynworkflow：graph 的 name 等于
// 节点 id，显示名只在节点启动时的 node_code 事件里。
func TestWorkflowViewUsesNodeCodeNameFallback(t *testing.T) {
	v := newWorkflowView()
	v.Apply(workflowViewEvent(t, `{"type":"graph","graph":{"nodes":{"fn":{"name":"fn"}},"edges":{},"start":["fn"]}}`))
	v.Apply(workflowViewEvent(t, `{"type":"node_code","nodeId":"fn","name":"Display Name","code":"..."}`))
	if got := v.Render(""); !strings.Contains(got, "- [ ] fn: Display Name") {
		t.Fatalf("node_code name fallback failed: %q", got)
	}
}

// TestWorkflowStateMark 验证状态到勾选框的映射。
func TestWorkflowStateMark(t *testing.T) {
	cases := map[string]string{
		"": "[ ]", "wait": "[ ]", "queue": "[ ]",
		"running": "[-]",
		"done":    "[X]", "error": "[X]", "terminated": "[X]",
	}
	for state, want := range cases {
		if got := workflowStateMark(state); got != want {
			t.Errorf("state %q: got %q want %q", state, got, want)
		}
	}
}

// TestRenderWorkflowResult 验证终值渲染：字符串原样、nil 为 null、其余紧凑 JSON。
func TestRenderWorkflowResult(t *testing.T) {
	if got := renderWorkflowResult("plain"); got != "plain" {
		t.Errorf("string result = %q", got)
	}
	if got := renderWorkflowResult(nil); got != "null" {
		t.Errorf("nil result = %q", got)
	}
	if got := renderWorkflowResult(map[string]any{"b": 1, "a": 2}); got != `{"a":2,"b":1}` {
		t.Errorf("object result = %q", got)
	}
}

// TestWorkflowViewLogsAreBounded 验证单节点日志行数上限（丢弃最早的并提示）。
func TestWorkflowViewLogsAreBounded(t *testing.T) {
	v := newWorkflowView()
	v.Apply(workflowViewEvent(t, `{"type":"graph","graph":{"nodes":{"n":{"name":"N"}},"edges":{},"start":["n"]}}`))
	for i := 0; i < workflowViewMaxLogsPerNode+5; i++ {
		v.Apply(workflowViewEvent(t, fmt.Sprintf(`{"type":"node_log","nodeId":"n","message":"line-%d"}`, i)))
	}
	got := v.Render("")
	if !strings.Contains(got, "(earlier log lines omitted)") {
		t.Fatalf("应提示丢弃的日志行: %q", got)
	}
	if n := strings.Count(got, "  line-"); n != workflowViewMaxLogsPerNode {
		t.Fatalf("日志行数 = %d, want %d", n, workflowViewMaxLogsPerNode)
	}
	if strings.Contains(got, "line-0\n") {
		t.Fatal("最早的日志应被丢弃")
	}
}
