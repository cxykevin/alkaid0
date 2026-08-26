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
		{"import dynworkflow as d", true},
		{"from dynworkflow import Flow", true},
		{"# import dynworkflow", false},
		{"x = 'from dynworkflow import Flow'", false},
		{"import dynworkflow_extra", false},
	}
	for _, tt := range tests {
		if got := containsDynworkflowImport(tt.code); got != tt.want {
			t.Errorf("%q: got %v want %v", tt.code, got, tt.want)
		}
	}
}
