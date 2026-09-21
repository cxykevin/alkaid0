//go:build linux

package sandbox

import (
	"os/exec"
	"strings"
	"testing"
)

// TestGenerateWritableMountsRemountBind 回归测试：可写目录的 remount 必须带 bind。
//
// 沙盒外层用 remount,ro,bind 把继承来的每个挂载项置为只读（只改挂载项自身的 ro
// 标志），内层恢复写权限也必须用 remount,rw,bind。不带 bind 的 remount 会尝试修改
// 整个 superblock 的 ro 标志，在 user namespace 中必然 EPERM（实测 rc=32）→ 可写
// 目录保持只读，命令写入报"只读文件系统"（TestVerifyWriteFile 的 OS 隔离分支、
// whoami 用的伪造 passwd 挂载都会因此失败）。
//
// 这里在真实的 user+mount namespace 里执行生产代码 generateWritableMounts 生成的
// 挂载命令，断言目录确实恢复可写。
func TestGenerateWritableMountsRemountBind(t *testing.T) {
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("系统缺少 unshare")
	}
	if err := exec.Command(unshare, "--user", "--map-root-user", "true").Run(); err != nil {
		t.Skipf("当前环境不支持非特权 user namespace: %v", err)
	}

	sb, err := New(Config{TmpDir: "/tmp", WorkDir: "/tmp", IsolationMode: IsolationOS})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	_, cmds := sb.generateWritableMounts()

	script := `
T=$(mktemp -d) || exit 3
mount --rbind / "$T" 2>/dev/null || { echo RBIND_FAIL; exit 3; }
mount -o remount,ro,bind "$T/tmp" 2>/dev/null || { echo RO_FAIL; exit 4; }
export ALK_WD_0="$T/tmp"
export ALK_WD_1="$T/tmp"
` + cmds + `
touch "$ALK_WD_0/.alk-writable-probe" 2>/dev/null && echo WRITE_OK || echo WRITE_FAIL
rm -f "$ALK_WD_0/.alk-writable-probe" 2>/dev/null
umount -R "$T" 2>/dev/null
rm -rf "$T" 2>/dev/null
`

	out, _ := exec.Command(unshare, "--user", "--map-root-user", "--mount", "sh", "-c", script).CombinedOutput()
	text := string(out)
	if strings.Contains(text, "RBIND_FAIL") || strings.Contains(text, "RO_FAIL") {
		t.Skipf("当前环境无法在 user namespace 中准备只读挂载: %s", text)
	}
	if !strings.Contains(text, "WRITE_OK") {
		t.Fatalf("可写目录在 user namespace 中仍不可写（remount 缺 bind？）: %s", text)
	}
}
