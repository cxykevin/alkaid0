package run

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWorkflowViewEndToEnd 用真实 Python + dynworkflow 跑一个纯节点工作流，验证
// stdout 握手 → 事件 → 视图渲染 → 临时对象内容（read 看到的内容）整条链路。
//
// 需要装有 dynworkflow 的解释器：设置 ALKAID0_TEST_WORKFLOW_PYTHON 指向它
// （例如 ~/.config/alkaid0/venv/bin/python）；未设置时跳过。
func TestWorkflowViewEndToEnd(t *testing.T) {
	python := os.Getenv("ALKAID0_TEST_WORKFLOW_PYTHON")
	if python == "" {
		t.Skip("set ALKAID0_TEST_WORKFLOW_PYTHON to a python with dynworkflow installed")
	}
	if _, err := os.Stat(python); err != nil {
		t.Skipf("python not found: %v", err)
	}
	script := strings.Join([]string{
		"import dynworkflow as d",
		"flow = d.Flow(\"e2e\", cache=False, report=True)",
		"",
		"@flow.node(\"Scan Docs\")",
		"def scan() -> d.NodeResult:",
		"    print(\"scanning\")",
		"    return d.Result({\"ok\": True})",
		"",
		"@flow.node(\"Report\")",
		"def report() -> d.NodeResult:",
		"    return d.Result(\"done\")",
		"",
		"flow.execute(scan())",
	}, "\n")

	view := newWorkflowView()
	var mu sync.Mutex
	var content string
	cwd := t.TempDir()
	req := &Request{
		Program:          python,
		Args:             []string{"-c", script},
		WorkDir:          cwd,
		Workspace:        cwd,
		Env:              os.Environ(),
		DisplayCommand:   "python (workflow view e2e)",
		BackgroundKind:   "workflow",
		InteractiveStdin: true,
		UpdateFn: workflowUpdateFn(view, func(c string) {
			mu.Lock()
			content = c
			mu.Unlock()
		}),
		WorkflowOutputFn: func(runID, visible string, events []WorkflowEvent) {
			for _, event := range events {
				view.Apply(event)
			}
		},
	}
	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result := job.Wait(ctx)
	if result == nil || !result.Success {
		t.Fatalf("workflow python failed: %#v", result)
	}

	mu.Lock()
	rendered := content
	mu.Unlock()
	for _, want := range []string{
		"- [X] scan: Scan Docs",
		"  scanning",
		"  Result: {\"ok\":true}",
		"- [ ] report: Report",
		workflowViewRawSeparator,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered content missing %q:\n%s", want, rendered)
		}
	}
}
