package run

import "testing"

func TestWorkflowOutputParser(t *testing.T) {
	var p WorkflowOutputParser
	visible, events := p.Feed([]byte("hello\n" + workflowPasteStart + "{\"type\":\"graph\",\"graph\":{}}\n" + workflowPasteEnd + "tail"))
	if visible != "hello\n" {
		t.Fatalf("visible=%q", visible)
	}
	if len(events) != 1 || events[0].Type != "graph" {
		t.Fatalf("events=%#v", events)
	}
	tail, more := p.Feed([]byte(workflowPasteStart[:4]))
	if tail != "t" || len(more) != 0 {
		t.Fatalf("partial start handling: %q %#v", tail, more)
	}
	tail, more = p.Feed([]byte(workflowPasteStart[4:] + "{\"type\":\"node\"}" + workflowPasteEnd))
	if tail != "ail" || len(more) != 1 || more[0].Type != "node" {
		t.Fatalf("split frame failed: %q %#v", tail, more)
	}
}

func TestContainsDynworkflowImport(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"import dynworkflow", true},
		{"import dynworkflow as d", true},
		{"import dynworkflow.flow", true},
		{"from dynworkflow import Flow", true},
		{"from dynworkflow.flow import Flow", true},
		{"import os, dynworkflow", true},
		{"import dynworkflow, os", true},
		{"import os; import dynworkflow", true},
		{"# import dynworkflow", false},
		{"x = 'from dynworkflow import Flow'", false},
		{"import dynworkflow_extra", false},
		{"import dynworkflow_extra, os", false},
		{"import os  # import dynworkflow", false},
	}
	for _, tt := range tests {
		if got := containsDynworkflowImport(tt.code); got != tt.want {
			t.Errorf("%q: got %v want %v", tt.code, got, tt.want)
		}
	}
}

// TestWorkflowOutputParserStripsRunMarker 验证 Flow.run() 的 \x1e dynworkflow \x1f
// 运行标记被整行剔除（含分片、行尾换行分片），且不影响后续握手帧解析。
func TestWorkflowOutputParserStripsRunMarker(t *testing.T) {
	var p WorkflowOutputParser
	if visible, events := p.Feed([]byte(workflowRunMarker + "\nhello")); visible != "" || len(events) != 0 {
		t.Fatalf("marker should be stripped: visible=%q events=%#v", visible, events)
	}
	if tail := p.Flush(); tail != "hello" {
		t.Fatalf("flush = %q", tail)
	}

	// 标记与换行分属两次 read：不能多出一行空行。
	var q WorkflowOutputParser
	if visible, _ := q.Feed([]byte(workflowRunMarker)); visible != "" {
		t.Fatalf("partial marker leaked: %q", visible)
	}
	if visible, _ := q.Feed([]byte("\nrest")); visible != "" {
		t.Fatalf("marker EOL handling leaked: %q", visible)
	}
	if tail := q.Flush(); tail != "rest" {
		t.Fatalf("flush after marker = %q", tail)
	}

	// 标记本身被切断在两次 read 之间。
	var s WorkflowOutputParser
	if visible, _ := s.Feed([]byte("\x1edynwork")); visible != "" {
		t.Fatalf("split marker leaked: %q", visible)
	}
	if visible, _ := s.Feed([]byte("flow\x1f\n")); visible != "" {
		t.Fatalf("split marker leaked: %q", visible)
	}

	// 标记后紧跟握手帧：帧仍按事件解析。
	var r WorkflowOutputParser
	visible, events := r.Feed([]byte(workflowRunMarker + "\n" + workflowPasteStart + "{\"type\":\"node\"}\n" + workflowPasteEnd))
	if visible != "" || len(events) != 1 || events[0].Type != "node" {
		t.Fatalf("frame after marker: visible=%q events=%#v", visible, events)
	}
}
