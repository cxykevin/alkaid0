package toolobj

import (
	"testing"
)

func TestToolsListInit(t *testing.T) {
	if ToolsList == nil {
		t.Error("ToolsList should be initialized")
	}
}

func TestScopesInit(t *testing.T) {
	if Scopes == nil {
		t.Error("Scopes should be initialized")
	}
}
