//go:build windows

package windows

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 回归测试（issue #9）：默认沙盒路径（隔离模式）下命令由 powershell/cmd 执行，
// 实际工作由其后代进程完成。只终止直接子进程时，后代进程会继续运行并继续持有
// 继承来的 stdout 句柄：kill 无效、任务永远停在 running。
// 修复后 Kill 通过 Job Object 终止整棵进程树，Wait 也会在排空上限内返回。
//
// 需要管理员权限与 ALKAID0_TEST_SANDBOX=true（与其他沙盒测试一致）。
// Windows CI runner 上 powershell 冷启动可能超过 10 秒，因此同步点一律用
// 握手文件，不做启动耗时的假设。

// waitForFile 等待握手文件出现。
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// waitCmd 等待命令结束，超时返回 false。
func waitCmd(cmd *Cmd, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func newKillTestCmd(t *testing.T, script string) (*Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Dir = t.TempDir()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	return cmd, &out
}

// TestKillProcessTree 验证 Kill 终止整棵进程树：
// 只杀直接子进程时，后代进程会继续存活并写入标记文件。
func TestKillProcessTree(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	ready := filepath.Join(t.TempDir(), "ready.txt")
	marker := filepath.Join(t.TempDir(), "alive.txt")
	t.Setenv("ALKAID0_READY_MARKER", ready)
	t.Setenv("ALKAID0_KILL_MARKER", marker)

	// 直接子进程 powershell 启动一个后代进程后自身继续等待；
	// 后代进程先写 ready 文件，2 秒后写 marker 文件。
	script := "Start-Process -FilePath powershell " +
		"-ArgumentList '-NoProfile','-Command','Set-Content -LiteralPath $env:ALKAID0_READY_MARKER -Value ready; Start-Sleep -Seconds 2; Set-Content -LiteralPath $env:ALKAID0_KILL_MARKER -Value alive' " +
		"-NoNewWindow; Start-Sleep -Seconds 300"

	cmd, _ := newKillTestCmd(t, script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !waitForFile(ready, 60*time.Second) {
		_ = cmd.Kill()
		t.Fatalf("后代进程未在 60s 内启动（powershell 不可用？）")
	}

	if err := cmd.Kill(); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	if !waitCmd(cmd, 30*time.Second) {
		t.Fatal("Kill 后 Wait 未返回：后代进程仍持有管道，任务卡死（issue #9 回归）")
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

// TestFinishedCommandWithDetachedDescendant 验证命令本身结束后，即使后代进程仍持有
// 输出管道句柄，Wait 也会在排空上限内返回（不会永远卡在 running）。
func TestFinishedCommandWithDetachedDescendant(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	heartbeat := filepath.Join(t.TempDir(), "heartbeat.txt")
	exited := filepath.Join(t.TempDir(), "exited.txt")
	stop := filepath.Join(t.TempDir(), "stop.txt")
	t.Setenv("ALKAID0_HEARTBEAT", heartbeat)
	t.Setenv("ALKAID0_EXITED", exited)
	t.Setenv("ALKAID0_STOP", stop)
	defer func() { _ = os.WriteFile(stop, []byte("stop"), 0o644) }()

	// 直接子进程启动后代进程后立即退出（写 exited 文件作为退出标记）；
	// 后代进程持续刷新心跳文件且不向 stdout 写内容，
	// 直到 stop 文件出现——它继承的 stdout 句柄因此在命令结束后依然被持有。
	script := "Start-Process -FilePath powershell " +
		"-ArgumentList '-NoProfile','-Command','while (-not (Test-Path -LiteralPath $env:ALKAID0_STOP)) { Set-Content -LiteralPath $env:ALKAID0_HEARTBEAT -Value (Get-Date -Format o); Start-Sleep -Milliseconds 300 }' " +
		"-NoNewWindow; Set-Content -LiteralPath $env:ALKAID0_EXITED -Value (Get-Date -Format o)"

	cmd, _ := newKillTestCmd(t, script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !waitForFile(heartbeat, 60*time.Second) {
		_ = cmd.Kill()
		t.Fatalf("后代进程未在 60s 内启动（powershell 不可用？）")
	}
	if !waitForFile(exited, 60*time.Second) {
		_ = cmd.Kill()
		t.Fatalf("直接子进程未在 60s 内退出")
	}
	exitTime := time.Now()

	if !waitCmd(cmd, 60*time.Second) {
		t.Fatal("Wait 未返回：后代进程持有管道导致任务卡死（issue #9 回归）")
	}

	// 至少等了排空上限（DrainTimeout）才返回：说明这次返回是排空兜底触发的，
	// 而不是同步等待后代进程退出。
	if waited := time.Since(exitTime); waited < 1500*time.Millisecond {
		t.Fatalf("Wait 在直接子进程退出后 %s 就返回了：后代进程未持有管道，测试前提不成立", waited)
	}

	// 任务结束后后代进程依然存活（心跳文件继续变化）→ 没有等它结束
	first, err := os.ReadFile(heartbeat)
	if err != nil {
		t.Fatalf("read heartbeat failed: %v", err)
	}
	changed := false
	for range 10 {
		time.Sleep(400 * time.Millisecond)
		cur, rerr := os.ReadFile(heartbeat)
		if rerr != nil {
			continue
		}
		if string(cur) != string(first) {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatalf("后代进程已不在运行：Wait 等待了后代进程退出，排空兜底未生效")
	}
}
