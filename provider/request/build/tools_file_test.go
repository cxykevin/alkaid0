package build

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// TestFileToolRegisteredByIndexLoad 验证 file 工具经插件导入 → AddIndex → Load
// 链路真正注册，参数完整，PostHook 已挂上（否则模型能看见工具却无法执行）。
func TestFileToolRegisteredByIndexLoad(t *testing.T) {
	setupBuildTest(t) // 内部执行 index.Load()

	tool := toolobj.GetTool("file")
	if tool == nil {
		t.Fatalf("tool %q is not registered by index.Load()", "file")
	}
	for _, name := range []string{"type", "src", "dst"} {
		if _, ok := tool.Parameters[name]; !ok {
			t.Fatalf("file tool is missing parameter %q: %#v", name, tool.Parameters)
		}
	}
	desc := tool.UserDescription
	for _, want := range []string{"move", "copy", "link", "@trash", "@temp/run/<id>", "virtual"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("file tool description does not mention %q", want)
		}
	}

	hooks := toolobj.GetToolHooks("file")
	hasPost := false
	for _, hook := range hooks {
		if hook.PostHook.Func != nil {
			hasPost = true
		}
	}
	if !hasPost {
		t.Fatal("file tool has no PostHook; the model could call it but nothing would execute")
	}
}
