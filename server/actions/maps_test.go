package actions

import (
	"testing"

	"github.com/cxykevin/alkaid0/ui/loop"
)

func TestReasonMap(t *testing.T) {
	expected := map[loop.StopReason]string{
		loop.StopReasonNone:        "end_turn",
		loop.StopReasonModel:       "end_turn",
		loop.StopReasonUser:        "cancelled",
		loop.StopReasonError:       "refusal",
		loop.StopReasonPendingTool: "end_turn",
	}

	for k, v := range expected {
		if ReasonMap[k] != v {
			t.Errorf("ReasonMap[%v] = %v, want %v", k, ReasonMap[k], v)
		}
	}
}

func TestToolNameToTypeMap(t *testing.T) {
	expected := map[string]string{
		"agent":            "other",
		"scope":            "other",
		"activate_agent":   "other",
		"deactivate_agent": "other",
		"edit":             "edit",
		"trace":            "read",
		"run":              "execute",
	}

	for k, v := range expected {
		if ToolNameToTypeMap[k] != v {
			t.Errorf("ToolNameToTypeMap[%s] = %s, want %s", k, ToolNameToTypeMap[k], v)
		}
	}
}
