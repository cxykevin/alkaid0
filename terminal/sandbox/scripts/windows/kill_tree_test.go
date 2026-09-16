//go:build windows

package windows

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestKillProcessTree 回归测试（issue #9）：默认沙盒路径（隔离模式）下，
// 命令由 powershell/cmd 执行，实际工作由其后代进程完成。只终止直接子进程时，
// 后代进程会继续运行并继续持有继承来的 stdout 句柄：kill 无效、任务永远停在 running。
// 修复后 Kill 通过 Job Object 终止整棵进程树。
//
// 需要管理员权限与 ALKAID0_TEST_SANDBOX=true（与其他沙盒测试一致）。
func TestKillProcessTree(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	marker := filepath.Join(t.TempDir(), "alive.txt")
	t.Setenv("ALKAID0_KILL_MARKER", marker)

	// 后代进程 3 秒后写标记文件；整棵树被终止则文件不会出现
	script := "Start-Process -FilePath powershell " +
		"-ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 3; Set-Content -LiteralPath $env:ALKAID0_KILL_MARKER -Value alive' " +
		"-NoNewWindow; Start-Sleep -Seconds 60"

	cmd := Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Dir = t.TempDir()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	if err := cmd.Kill(); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Wait did not return after Kill (descendant process still holds the pipe)")
	}

	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("descendant process is still alive and wrote %s: process tree was not terminated", marker)
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

	// 直接子进程立即退出，后代进程存活 60 秒并一直持有继承来的 stdout
	script := "Start-Process -FilePath powershell " +
		"-ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 60' -NoNewWindow"

	cmd := Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Dir = t.TempDir()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil error (command exited 0), got %v", err)
		}
		t.Logf("Wait returned in %s", time.Since(start))
	case <-time.After(20 * time.Second):
		t.Fatal("Wait did not return: pipe drain waited for the descendant process")
	}
}
