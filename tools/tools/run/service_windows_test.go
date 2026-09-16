//go:build windows

package run

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Windows 专有回归测试（issue #9）：
//
// Windows 没有进程组语义，os.Process.Kill 只能终止直接子进程。而 run 工具执行的
// 是 powershell/cmd，真正干活的是其后代（npm、node、python、常驻服务……）。
// 修复前表现为：kill 命令与 ESC 都杀不掉命令、后代进程继续持有继承来的
// stdout 管道句柄，导致任务永远停在 running、程序无法自行结束。
//
// 这些用例不需要管理员权限（Sandbox: false → 无隔离执行路径），
// 因此在 GitHub Actions 的 windows-latest 上会真实执行。

// windowsKillScript 让直接子进程（powershell）再启动一个后代进程（powershell），
// 后代 3 秒后写入标记文件；若进程树被正确终止，标记文件永远不会出现。
// 标记路径通过环境变量传入，避免多层引号转义。
const windowsKillScript = "Start-Process -FilePath powershell " +
	"-ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 3; Set-Content -LiteralPath $env:ALKAID0_KILL_MARKER -Value alive' " +
	"-NoNewWindow; Start-Sleep -Seconds 60"

// waitJobDone 等待任务结束；超时说明任务卡死（kill 后仍停在 running）。
func waitJobDone(t *testing.T, job *Job, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		job.Wait(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("job %s did not finish within %s after kill (hang)", job.ID, timeout)
	}
}

// TestServiceKillRunProcessTree 验证 kill 终止整棵进程树：
// 只杀直接子进程时，后代进程会继续存活并写入标记文件。
func TestServiceKillRunProcessTree(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "alive.txt")
	t.Setenv("ALKAID0_KILL_MARKER", marker)

	job, err := Default.Submit(context.Background(), testRunRequest(windowsKillScript))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// 给直接子进程与后代进程一点启动时间
	time.Sleep(1500 * time.Millisecond)

	if err := Default.Kill(job.Workspace, job.ID); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	waitJobDone(t, job, 15*time.Second)

	if job.Status() != JobKilled {
		t.Errorf("expected job killed, got %v", job.Status())
	}

	// 超过后代进程写标记的时间点后再检查：文件存在说明后代仍在运行
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("descendant process is still alive and wrote %s: process tree was not terminated", marker)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker failed: %v", err)
	}
}

// TestServiceFinishedCommandReleasesPipes 验证命令结束后后代进程仍持有管道时
// 任务不会卡死：直接子进程退出（成功）但后代继续持有 stdout 句柄，
// 修复前 Wait 会一直等不到 EOF，任务永远停在 running。
func TestServiceFinishedCommandReleasesPipes(t *testing.T) {
	// 直接子进程立刻退出，后代进程存活 60 秒并一直持有继承来的 stdout
	script := "Start-Process -FilePath powershell " +
		"-ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 60' -NoNewWindow"

	job, err := Default.Submit(context.Background(), testRunRequest(script))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	start := time.Now()
	waitJobDone(t, job, 20*time.Second)
	elapsed := time.Since(start)

	if job.Status() != JobFinished {
		t.Errorf("expected job finished, got %v", job.Status())
	}
	result := job.Wait(context.Background())
	if result == nil || !result.Success {
		t.Errorf("expected success result (command itself exited 0), got %+v", result)
	}
	if elapsed > 15*time.Second {
		t.Errorf("job took %s to finish: pipe drain waited for the descendant process", elapsed)
	}
	t.Logf("job finished in %s", elapsed)
}

// TestServiceKillRunProcessTreeTwice 验证重复 kill 幂等且不 panic（进程未启动时
// Kill 读取到空的 Process 句柄也不能崩）。
func TestServiceKillRunProcessTreeTwice(t *testing.T) {
	job, err := Default.Submit(context.Background(), testRunRequest("Start-Sleep -Seconds 60"))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if err := Default.Kill(job.Workspace, job.ID); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	waitJobDone(t, job, 15*time.Second)
	// 已结束的任务再次 kill 不应 panic/报错
	if err := Default.Kill(job.Workspace, job.ID); err != nil {
		t.Errorf("second Kill returned error: %v", err)
	}
	if job.Status() != JobKilled {
		t.Errorf("expected job killed, got %v", job.Status())
	}
}
