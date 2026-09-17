//go:build windows

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Windows 沙盒路径（IsolationOS）回归测试，需要管理员权限：
// 设置 ALKAID0_TEST_SANDBOX=true 启用（与其它沙盒测试一致）。

// funcWriter 模拟 run 工具的 writerFunc：函数类型，不可比较。
type funcWriter func([]byte) (int, error)

func (f funcWriter) Write(p []byte) (int, error) { return f(p) }

func waitSandboxFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func newSandboxForTest(t *testing.T, dir string, extraEnv ...string) *Sandbox {
	t.Helper()
	sb, err := New(Config{
		WorkDir:       dir,
		WritableDirs:  []string{dir},
		Env:           append(os.Environ(), extraEnv...),
		IsolationMode: IsolationOS,
	})
	if err != nil {
		t.Fatalf("New sandbox failed: %v", err)
	}
	return sb
}

// TestSandboxKillProcessTree 回归：沙盒命令由 powershell/cmd 执行，真正干活的是其后代。
// 只终止直接子进程时，后代进程会继续运行（并继续持有继承来的 stdout 句柄）——
// 表现为 kill 无效、任务永远停在 running。修复后 Kill 通过 Job Object 终止整棵进程树。
func TestSandboxKillProcessTree(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready.txt")
	marker := filepath.Join(dir, "alive.txt")

	// 直接子进程 powershell 启动一个后代进程后自身继续等待；
	// 后代进程先写 ready 文件，2 秒后写 marker 文件。
	// 进程树被正确终止时 marker 永远不会出现。
	script := "Start-Process -FilePath powershell " +
		"-ArgumentList '-NoProfile','-Command','Set-Content -LiteralPath $env:ALKAID0_READY_MARKER -Value ready; Start-Sleep -Seconds 2; Set-Content -LiteralPath $env:ALKAID0_KILL_MARKER -Value alive' " +
		"-NoNewWindow; Start-Sleep -Seconds 300"

	sb := newSandboxForTest(t, dir, "ALKAID0_READY_MARKER="+ready, "ALKAID0_KILL_MARKER="+marker)
	cmd, err := sb.Execute("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	var out bytes.Buffer
	cmd.SetStdout(&out)
	cmd.SetStderr(&out)

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !waitSandboxFile(ready, 60*time.Second) {
		_ = cmd.Kill()
		t.Fatalf("后代进程未在 60s 内启动（沙盒输出: %q）", out.String())
	}

	if err := cmd.Kill(); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Kill 后 Wait 未返回：后代进程仍持有管道，任务卡在 running（issue #9 回归）")
	}

	// 后代进程写 marker 的时间点（ready 之后 2 秒）早已过去：文件存在说明
	// 后代进程仍在运行，即 Kill 只终止了直接子进程。
	time.Sleep(3500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("后代进程仍在运行并写入了 %s：进程树未被终止", marker)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker failed: %v", err)
	}
}

// TestSandboxSameWriterStdoutStderr 回归：run 工具把 stdout/stderr 设为同一个
// 函数类型 writer（不可比较），Start 里直接 a == b 会 panic，
// 导致 Windows 沙盒路径每条命令都失败（runtime error: comparing uncomparable type）。
func TestSandboxSameWriterStdoutStderr(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	dir := t.TempDir()
	sb := newSandboxForTest(t, dir)
	cmd, err := sb.Execute("cmd", "/C", "echo alkaid0-sandbox-ok")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var buf bytes.Buffer
	w := funcWriter(func(p []byte) (int, error) { return buf.Write(p) })
	cmd.SetStdout(w)
	cmd.SetStderr(w) // 同一个不可比较的 writer：修复前此处 panic

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed（writer 比较 panic 未修复？）: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait failed: %v (output=%q)", err, buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("alkaid0-sandbox-ok")) {
		t.Errorf("expected command output, got %q", buf.String())
	}
}
