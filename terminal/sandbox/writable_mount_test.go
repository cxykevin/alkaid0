//go:build linux

package sandbox

import (
	"os/exec"
	"strings"
	"testing"
)

// TestGeneratePolicyMountsRemountBind 回归测试：可写目录的 remount 必须带 bind。
//
// 沙盒外层用 remount,ro,bind 把继承来的每个挂载项置为只读（只改挂载项自身的 ro
// 标志），内层恢复写权限也必须用 remount,rw,bind。不带 bind 的 remount 会尝试修改
// 整个 superblock 的 ro 标志，在 user namespace 中必然 EPERM（实测 rc=32）→ 可写
// 目录保持只读，命令写入报"只读文件系统"（TestVerifyWriteFile 的 OS 隔离分支、
// whoami 用的伪造 passwd 挂载都会因此失败）。
//
// 这里在真实的 user+mount namespace 里执行生产代码 generatePolicyMounts 生成的
// 挂载命令，断言目录确实恢复可写。
func TestGeneratePolicyMountsRemountBind(t *testing.T) {
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
	// 只保留"临时目录可写"这一条规则：其余封存/只读规则依赖真实环境，
	// 与本次要验证的 remount 语义无关。
	sb.policy = &Policy{}
	sb.policy.add("/tmp", AccessReadWrite)

	exports, cmds := sb.generatePolicyMounts()
	if !strings.Contains(cmds, "remount,rw,bind") {
		t.Fatalf("可写目录的 remount 缺少 bind（生成的脚本）:\n%s", cmds)
	}

	script := `
T=$(mktemp -d) || exit 3
mount --rbind / "$T" 2>/dev/null || { echo RBIND_FAIL; exit 3; }
# 把 /tmp 自身变成挂载项再置只读：模拟沙盒外层把继承来的挂载项全部只读化
mount --rbind /tmp /tmp 2>/dev/null || { echo RBIND_FAIL; exit 3; }
mount -o remount,ro,bind /tmp 2>/dev/null || { echo RO_FAIL; exit 4; }
` + exports + `
` + cmds + `
touch "$ALK_P0/.alk-writable-probe" 2>/dev/null && echo WRITE_OK || echo WRITE_FAIL
rm -f "$ALK_P0/.alk-writable-probe" 2>/dev/null
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

// TestPolicyMountsSealLayout 校验封存（deny / list-only）脚本的生成结构：
// 真实内容必须在 tmpfs 覆盖之前 bind 到 scratch，之后才能安全地建占位条目。
func TestPolicyMountsSealLayout(t *testing.T) {
	sb, err := New(Config{TmpDir: "/tmp", WorkDir: "/tmp", IsolationMode: IsolationOS})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	sb.policy = &Policy{}
	sb.policy.add("/tmp/alk-seal-demo", AccessListOnly)
	sb.policy.add("/tmp/alk-seal-demo/skills", AccessReadWrite)
	sb.policy.add("/tmp/alk-seal-demo/images", AccessReadOnly)

	_, cmds := sb.generatePolicyMounts()

	mountTmpfs := strings.Index(cmds, "mount -t tmpfs tmpfs \"$ALK_P0\"")
	preBind := strings.Index(cmds, "mount --rbind \"$ALK_P0\" \"$_ALK_SCRATCH/s0\"")
	if preBind < 0 || mountTmpfs < 0 {
		t.Fatalf("封存脚本缺少必要的挂载步骤:\n%s", cmds)
	}
	if preBind > mountTmpfs {
		t.Fatalf("必须在 tmpfs 覆盖之前 bind 真实内容，否则真实内容就取不到了:\n%s", cmds)
	}
	if !strings.Contains(cmds, "ln -s") {
		t.Fatalf("list-only 封存应生成占位条目:\n%s", cmds)
	}
	if !strings.Contains(cmds, "rm -f \"$ALK_P") {
		t.Fatalf("封存下的可写子目录必须能替换占位条目:\n%s", cmds)
	}
	// 封存点必须最终置为只读（否则沙盒内可在封存视图里新建文件）
	if strings.Count(cmds, "mount -o remount,ro,bind \"$ALK_P0\"") == 0 {
		t.Fatalf("封存点最后必须 remount 成只读:\n%s", cmds)
	}
}
