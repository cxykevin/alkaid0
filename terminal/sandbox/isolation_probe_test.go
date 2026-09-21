package sandbox

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// requireOSIsolation 在环境不支持 OS 级隔离时跳过用例。
//
// GitHub Actions 的 ubuntu runner 禁用了非特权 user namespace，任何 unshare
// 调用都会以 "unshare: write failed /proc/self/uid_map: Operation not permitted"
// 失败；这是宿主机能力问题而不是被测代码的问题。能用的环境（本地开发机、允许
// userns 的 CI）里这些用例仍然真正执行，因此不能用"默认跳过"掩盖。
func requireOSIsolation(t *testing.T) {
	t.Helper()
	// 目前只有 Linux 实现依赖非特权 userns；darwin 走 sandbox-exec，能力探测另论。
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skipf("环境缺少 unshare，跳过 OS 隔离用例: %v", err)
	}
	out, err := exec.Command("unshare", "--user", "--map-root-user", "true").CombinedOutput()
	if err != nil {
		t.Skipf("当前环境不允许非特权 user namespace（%v: %s），跳过 OS 隔离用例",
			err, strings.TrimSpace(string(out)))
	}
}
