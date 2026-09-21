//go:build windows

package sandbox

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestIsolateNoneCmdSuspendedStartResumes 回归测试：隔离关闭路径改为
// CREATE_SUSPENDED 创建进程、先加入 Job Object 再恢复主线程。命令必须能正常
// 恢复运行并产生输出；若恢复逻辑缺失，进程会永远挂起、Wait 永不返回。
func TestIsolateNoneCmdSuspendedStartResumes(t *testing.T) {
	e := createIsolateNoneCmd(context.Background(), "cmd", []string{"/C", "echo alkaid0-resumed"}, nil, "")
	var out bytes.Buffer
	e.SetStdout(&out)
	e.SetStderr(&out)

	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := e.Wait(); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if !strings.Contains(out.String(), "alkaid0-resumed") {
		t.Fatalf("输出中缺少标记，进程可能未被恢复: %q", out.String())
	}
}
