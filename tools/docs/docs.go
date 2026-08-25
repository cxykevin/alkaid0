// Package docs provides built-in read-only documentation resources.
package docs

import (
	_ "embed"
)

const workflowPath = "@docs/run/workflow"

//go:embed docs/SKILL.md
var workflow string

// IsPath reports whether path names a supported built-in document.
func IsPath(path string) bool {
	return path == workflowPath
}

// IsDocsPath reports whether path is in the reserved @docs namespace.
func IsDocsPath(path string) bool {
	return path == "@docs" || len(path) > len("@docs/") && path[:len("@docs/")] == "@docs/"
}

// Read returns the embedded document for a supported path.
func Read(path string) (string, bool) {
	if path != workflowPath {
		return "", false
	}
	return workflow, true
}

// WorkflowPath is the virtual path of the embedded workflow documentation.
func WorkflowPath() string {
	return workflowPath
}
