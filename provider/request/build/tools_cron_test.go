package build

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// TestCronToolRegisteredByIndexLoad 验证 cron 包注册的工具能被启动时的
// index.Load() 真正加载出来，并且说明/参数覆盖了关键约束。
func TestCronToolRegisteredByIndexLoad(t *testing.T) {
	setupBuildTest(t) // 内部执行 index.Load()

	tool := toolobj.GetTool("cron")
	if tool == nil {
		t.Fatalf("tool %q is not registered by index.Load()", "cron")
	}
	for _, key := range []string{"type", "id", "cron", "prompt", "run_once"} {
		if _, ok := tool.Parameters[key]; !ok {
			t.Fatalf("cron tool is missing parameter %q: %#v", key, tool.Parameters)
		}
	}
	if !tool.Parameters["type"].Required {
		t.Fatal("cron tool parameter type must be required")
	}
	for _, want := range []string{"@cron", "run_once", "200"} {
		if !strings.Contains(tool.UserDescription, want) {
			t.Fatalf("cron tool description does not mention %q: %q", want, tool.UserDescription)
		}
	}
}
