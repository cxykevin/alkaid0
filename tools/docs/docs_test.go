package docs

import "testing"

func TestReadWorkflow(t *testing.T) {
	content, ok := Read(WorkflowPath())
	if !ok || content == "" {
		t.Fatal("embedded workflow documentation is empty")
	}
	if !contains(content, "# dynworkflow Usage") {
		t.Fatal("embedded workflow documentation has unexpected content")
	}
}

func TestReadRejectsUnknownPath(t *testing.T) {
	if _, ok := Read("@docs/unknown"); ok {
		t.Fatal("unknown documentation path unexpectedly resolved")
	}
	if !IsDocsPath("@docs/unknown") || IsPath("@docs/unknown") {
		t.Fatal("documentation namespace/path classification is incorrect")
	}
}

func contains(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
