//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestWrapIsolateNoneCommandWithoutRunAs 未指定用户时命令原样执行。
func TestWrapIsolateNoneCommandWithoutRunAs(t *testing.T) {
	s := &Sandbox{}
	name, args := wrapIsolateNoneCommand(s, "bash", []string{"-c", "id"})
	if name != "bash" || len(args) != 2 || args[0] != "-c" || args[1] != "id" {
		t.Fatalf("未配置用户时应原样返回，得到 %s %v", name, args)
	}
}

// TestSandboxRunOwnerPrefersConfiguredUser 沙盒内降权身份优先取配置用户。
func TestSandboxRunOwnerPrefersConfiguredUser(t *testing.T) {
	s := &Sandbox{runAs: &runAsUser{Name: "alice", UID: 1234, GID: 5678}}
	uid, gid, name := s.sandboxRunOwner()
	if uid != 1234 || gid != 5678 || name != "alice" {
		t.Fatalf("sandboxRunOwner = (%d,%d,%q)，期望 (1234,5678,alice)", uid, gid, name)
	}
}

// TestPrepareRunAsSameUserIsSilent 目标用户即当前用户：无需切换，也不告警。
func TestPrepareRunAsSameUserIsSilent(t *testing.T) {
	ru := &runAsUser{Name: "self", UID: os.Geteuid(), GID: os.Getegid(), sameAsCurrent: true}
	use, reason := prepareRunAs(ru)
	if use || reason != "" {
		t.Fatalf("同用户应静默忽略，得到 use=%v reason=%q", use, reason)
	}
}

// TestPrepareRunAsRequiresRoot 非 root 切换到其它用户时给出告警原因（回退当前用户）。
func TestPrepareRunAsRequiresRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("当前以 root 运行，该分支不适用")
	}
	use, reason := prepareRunAs(&runAsUser{Name: "other", UID: 0})
	if use || reason == "" {
		t.Fatalf("非 root 应回退并给出原因，得到 use=%v reason=%q", use, reason)
	}
}

// TestWrapIsolateNoneCommandRunsAsTargetUser 端到端验证：沙盒外包装后的命令
// 确实以目标用户身份运行（需要 root 与 setpriv）。
func TestWrapIsolateNoneCommandRunsAsTargetUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("需要 root 权限才能切换用户")
	}
	target, err := user.Lookup("nobody")
	if err != nil {
		t.Skipf("系统中没有 nobody 用户: %v", err)
	}
	if _, err := exec.LookPath("setpriv"); err != nil {
		t.Skipf("未安装 setpriv: %v", err)
	}
	uid, err := strconv.Atoi(target.Uid)
	if err != nil {
		t.Fatalf("nobody 的 uid 无效: %v", err)
	}
	gid, err := strconv.Atoi(target.Gid)
	if err != nil {
		t.Fatalf("nobody 的 gid 无效: %v", err)
	}

	s := &Sandbox{runAs: &runAsUser{Name: target.Username, UID: uid, GID: gid, Home: target.HomeDir}}
	name, args := wrapIsolateNoneCommand(s, "id", []string{"-u"})

	out, err := exec.Command(name, args...).Output()
	if err != nil {
		t.Fatalf("执行包装后的命令失败: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != target.Uid {
		t.Fatalf("包装后命令以 uid=%s 运行，期望 %s", got, target.Uid)
	}
}

// TestOSIsolationRunsAsConfiguredUser 沙盒内命令以配置指定的用户运行。
//
// 覆盖配置用户（Agents.User）的沙盒内路径：mount.sh 依据 ALK_RUN_UID/ALK_RUN_GID/
// ALK_RUN_USER 做 setpriv 降权，并伪造 /etc/passwd/group 让 id/whoami 显示目标用户。
func TestOSIsolationRunsAsConfiguredUser(t *testing.T) {
	requireOSIsolation(t)
	if os.Geteuid() != 0 {
		t.Skip("需要 root 权限才能让沙盒命令以其它用户运行")
	}
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	target, err := user.Lookup("nobody")
	if err != nil {
		t.Skipf("系统中没有 nobody 用户: %v", err)
	}
	// 降权后的进程需要能进入工作目录（mount.sh 会 cd 到 ALK_WORKDIR），
	// 因此直接用系统临时目录下的 0755 目录：t.TempDir() 的父目录是 0700，
	// 其它用户无法穿过；用户家目录路径同理。
	wd, err := os.MkdirTemp("", "alkaid0-sandbox-runas-")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(wd) })
	if err := os.Chmod(wd, 0o755); err != nil {
		t.Fatalf("Chmod workdir failed: %v", err)
	}

	sb, err := New(Config{
		WorkDir:       wd,
		IsolationMode: IsolationOS,
		User:          target.Username,
		Timeout:       20 * time.Second,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	cmd, err := sb.Execute("sh", "-c", "id -un && id -u")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	cmd.SetStdout(&stdout)
	cmd.SetStderr(&stderr)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Run failed: %v\nstderr:\n%s", err, stderr.String())
	}

	lines := strings.Fields(strings.TrimSpace(stdout.String()))
	if len(lines) < 2 {
		t.Fatalf("沙盒内输出异常:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if lines[0] != target.Username {
		t.Errorf("沙盒内 id -un = %q，期望 %q", lines[0], target.Username)
	}
	if lines[1] != target.Uid {
		t.Errorf("沙盒内 id -u = %q，期望 %q", lines[1], target.Uid)
	}
}

// TestResolveGroupIDRoot 按组名与数字 gid 解析到同一个 root 组。
func TestResolveGroupIDRoot(t *testing.T) {
	name, gid, err := resolveGroupID("root")
	if err != nil {
		t.Skipf("系统中没有 root 组: %v", err)
	}
	byIDName, byIDGID, err := resolveGroupID(strconv.Itoa(gid))
	if err != nil {
		t.Fatalf("按数字 gid 解析失败: %v", err)
	}
	if name != byIDName || gid != byIDGID {
		t.Fatalf("按名/按 gid 解析结果不一致: %q(%d) vs %q(%d)", name, gid, byIDName, byIDGID)
	}
}

// TestResolveGroupIDUnknown 用户组不存在时返回错误（调用方回退主组并告警）。
func TestResolveGroupIDUnknown(t *testing.T) {
	if _, _, err := resolveGroupID("alkaid0-no-such-group-xyz"); err == nil {
		t.Fatalf("未知用户组应返回错误")
	}
}

// TestResolveRunAsUserWithGroup 指定用户组时 GID 取该组（而非用户主组）。
func TestResolveRunAsUserWithGroup(t *testing.T) {
	target, err := user.Lookup("nobody")
	if err != nil {
		t.Skipf("系统中没有 nobody 用户: %v", err)
	}
	groupName, groupGID, err := resolveGroupID("root")
	if err != nil {
		t.Skipf("系统中没有 root 组: %v", err)
	}
	ru, err := resolveRunAsUser(target.Username, groupName)
	if err != nil {
		t.Fatalf("resolveRunAsUser failed: %v", err)
	}
	uid, err := strconv.Atoi(target.Uid)
	if err != nil {
		t.Fatalf("nobody 的 uid 无效: %v", err)
	}
	if ru.UID != uid {
		t.Errorf("UID = %d，期望 %d", ru.UID, uid)
	}
	if ru.Group != groupName || ru.GID != groupGID {
		t.Errorf("Group/GID = %q/%d，期望 %q/%d", ru.Group, ru.GID, groupName, groupGID)
	}
}
