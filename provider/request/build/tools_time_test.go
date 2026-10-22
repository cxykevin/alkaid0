package build

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// TestTimeToolRegisteredByIndexLoad 验证 date 包注册的 time 工具能被启动时的
// index.Load() 真正加载出来（插件导入 → AddIndex → Load 链路），且参数为空。
//
// 这里只查注册表，不调用 Tools() 构建完整工具表：后者会执行各工具的 PreHook，
// agent 等工具的 PreHook 需要完整会话环境，不属于本用例的验证范围。
func TestTimeToolRegisteredByIndexLoad(t *testing.T) {
	setupBuildTest(t) // 内部执行 index.Load()

	tool := toolobj.GetTool("time")
	if tool == nil {
		t.Fatalf("tool %q is not registered by index.Load()", "time")
	}
	if len(tool.Parameters) != 0 {
		t.Fatalf("time tool parameters = %#v, want empty", tool.Parameters)
	}
	if !strings.Contains(tool.UserDescription, "GMT") {
		t.Fatalf("time tool description does not document the GMT offset: %q", tool.UserDescription)
	}
}
