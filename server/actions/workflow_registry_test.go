package actions

import (
	"testing"

	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
)

// TestWorkflowMethodRegistry 验证 workflow 的协议面：枚举统一走 terminal/list 与
// terminal/history 的 workflow 标记，不再注册 workflow/list；控制与查询方法保持注册。
func TestWorkflowMethodRegistry(t *testing.T) {
	srv := jsonrpc.New()
	InitFuncs(srv)

	const listMethod = "alk.cxykevin.top/session/terminal/workflow/list"
	if _, ok := srv.Methods[listMethod]; ok {
		t.Fatalf("%s 不应注册：workflow 枚举统一走 terminal/list 与 terminal/history 的 workflow 标记", listMethod)
	}
	for _, method := range []string{
		"alk.cxykevin.top/session/terminal/workflow/status",
		"alk.cxykevin.top/session/terminal/workflow/input",
		"alk.cxykevin.top/session/terminal/workflow/stop",
		"alk.cxykevin.top/session/terminal/list",
		"alk.cxykevin.top/session/terminal/history",
	} {
		if _, ok := srv.Methods[method]; !ok {
			t.Errorf("%s 应保持注册", method)
		}
	}
}
