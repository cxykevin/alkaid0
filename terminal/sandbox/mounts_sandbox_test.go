//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// 挂载表在真实隔离环境中的落地效果：mount 之前目标目录只读，
// 登记之后目标目录可写，且写入落到宿主的真实路径。
// 前置条件与 policy_sandbox_test.go 相同：ALKAID0_TEST_SANDBOX=true。
func TestMountTargetWritableInRealSandbox(t *testing.T) {
	sandboxSupported(t)
	root := policyTestRoot(t)
	ws := filepath.Join(root, "ws")
	sub := filepath.Join(ws, "sub")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{sub, outside, filepath.Join(ws, ".alkaid0")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{WorkDir: sub, Workspace: ws, TmpDir: os.TempDir(), IsolationMode: IsolationOS}

	run := func(sb *Sandbox, script string) error {
		cmd, err := sb.Execute("sh", "-c", script)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		var buf bytes.Buffer
		cmd.SetStdout(&buf)
		cmd.SetStderr(&buf)
		err = cmd.Run()
		t.Logf("script %q -> err=%v output=%s", script, err, buf.String())
		return err
	}

	before, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if err := run(before, "touch "+outside+"/before.txt"); err == nil {
		t.Fatalf("outside must be read-only before mounting")
	}

	if err := AddMount(ws, "outside", outside); err != nil {
		t.Fatalf("AddMount failed: %v", err)
	}
	after, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if err := run(after, "touch "+outside+"/after.txt && echo ok > "+outside+"/after.txt"); err != nil {
		t.Fatalf("mounted target must be writable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "after.txt")); err != nil {
		t.Fatalf("write did not land on the host: %v", err)
	}
}
