package sandbox

import (
	"bytes"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestOSIsolationWorkDirOwner 回归测试：沙盒命令应以工作目录实际属主身份运行。
//
// 背景：服务以 root 运行时，沙盒 unshare --map-root-user 将 user namespace 的 uid 0
// 映射为宿主 root，但 chroot 内 rbind 的挂载 superblock 属于宿主 user namespace，
// 子 namespace 进程无宿主 capabilities，且 uid 与工作目录属主（如 cxykevin:1000）
// 的 kuid 不匹配，导致访问权限受限（700）的工作目录失败（cd: 不是目录）。
//
// 修复：root 场景使用双重 uid 映射（0→0 保留 mount 能力 + owner→owner 供降权后访问），
// 并在 mount.sh 中通过 setpriv 降权到工作目录属主再执行命令。
// 该测试在普通用户与 root 下均应通过。
func TestOSIsolationWorkDirOwner(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}

	// 期望身份：工作目录属主的用户名
	info, err := os.Stat(wd)
	if err != nil {
		t.Fatalf("Stat workdir failed: %v", err)
	}
	expectedUser := "nobody"
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		if u, uerr := user.LookupId(strconv.Itoa(int(sys.Uid))); uerr == nil {
			expectedUser = u.Username
		}
	}
	t.Logf("workdir: %s, 期望属主: %s", wd, expectedUser)

	cfg := Config{
		WorkDir:       wd,
		IsolationMode: IsolationOS,
		Timeout:       15 * time.Second,
	}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	cmd, err := sb.Execute("bash", "-c", "pwd && whoami && free -h | head -1")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	cmd.SetStdout(&stdout)
	cmd.SetStderr(&stderr)

	err = cmd.Run()
	out := stdout.String()
	t.Logf("stdout:\n%s", out)
	if err != nil {
		t.Fatalf("Run failed: %v\nstderr:\n%s", err, stderr.String())
	}

	// 1. pwd 应回到工作目录
	if !strings.Contains(out, wd) {
		t.Errorf("pwd 应为 %q，输出中未包含", wd)
	}

	// 2. whoami 应为工作目录属主（而非 root）
	if !strings.Contains(out, expectedUser) {
		t.Errorf("whoami 应为 %q，实际输出：\n%s", expectedUser, out)
	}
}
