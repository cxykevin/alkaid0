//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeEtcFiles 返回给定目录下 mount.sh 伪造 passwd/group 所用的临时文件。
func fakeEtcFiles(dir string) map[string]struct{} {
	found := map[string]struct{}{}
	for _, pattern := range []string{".alk-sandbox-etc-password-*", ".alk-sandbox-etc-group-*"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, m := range matches {
			found[m] = struct{}{}
		}
	}
	return found
}

// fakeEtcSnapshot 汇总所有可能出现伪造文件的位置（chroot 内 /tmp 即宿主 /tmp）。
func fakeEtcSnapshot() map[string]struct{} {
	all := fakeEtcFiles("/tmp")
	for f := range fakeEtcFiles(os.TempDir()) {
		all[f] = struct{}{}
	}
	return all
}

// TestOSIsolationNoFakeEtcResidue 回归测试：沙盒在 chroot 内伪造的 passwd/group
// 文件落在宿主临时目录（/tmp 属于可写目录白名单，会被 rbind 进 chroot 并 remount,rw），
// 命令结束后必须立即删除，不能在宿主 /tmp 留下 .alk-sandbox-etc-* 残留。
//
// 改造前 mount.sh 只在 trap 里删 $T，伪造文件永久残留在宿主 /tmp。
func TestOSIsolationNoFakeEtcResidue(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	before := fakeEtcSnapshot()

	sb, err := New(Config{
		WorkDir:       t.TempDir(),
		IsolationMode: IsolationOS,
		Timeout:       30 * time.Second,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	cmd, err := sb.Execute("true")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	var stdout, stderr bytes.Buffer
	cmd.SetStdout(&stdout)
	cmd.SetStderr(&stderr)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Run failed: %v (stderr: %s)", err, stderr.String())
	}

	for f := range fakeEtcSnapshot() {
		if _, existed := before[f]; !existed {
			t.Errorf("沙盒命令结束后宿主临时目录残留伪造的 passwd/group 文件: %s", f)
		}
	}
}
