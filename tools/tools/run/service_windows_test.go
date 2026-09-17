//go:build windows

package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
//
// 注意：Windows CI runner 上 powershell 冷启动可能超过 10 秒，因此一律用
// "握手文件出现 / 任务结束" 作为同步点，不做绝对耗时的断言。

// waitForFile 等待握手文件出现（进程已启动/已退出），返回是否出现。
func waitForFile(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

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
		t.Fatalf("job %s did not finish within %s: stuck in running (issue #9)", job.ID, timeout)
	}
}

// TestServiceKillRunProcessTree 验证 kill 终止整棵进程树：
// 只杀直接子进程时，后代进程会继续存活并写入标记文件。
func TestServiceKillRunProcessTree(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready.txt")
	marker := filepath.Join(dir, "alive.txt")
	t.Setenv("ALKAID0_READY_MARKER", ready)
	t.Setenv("ALKAID0_KILL_MARKER", marker)

	// 直接子进程 powershell 启动一个后代进程后自身继续等待；
	// 后代进程启动后先写 ready 文件，2 秒后写 marker 文件。
	// 进程树被正确终止时 marker 永远不会出现。
	script := "Start-Process -FilePath powershell " +
		"-ArgumentList '-NoProfile','-Command','Set-Content -LiteralPath $env:ALKAID0_READY_MARKER -Value ready; Start-Sleep -Seconds 2; Set-Content -LiteralPath $env:ALKAID0_KILL_MARKER -Value alive' " +
		"-NoNewWindow; Start-Sleep -Seconds 300"

	job, err := Default.Submit(context.Background(), testRunRequest(script))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	if !waitForFile(t, ready, 60*time.Second) {
		t.Fatalf("后代进程未在 60s 内启动（powershell 不可用？）")
	}

	if err := Default.Kill(job.Workspace, job.ID); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	waitJobDone(t, job, 30*time.Second)

	if job.Status() != JobKilled {
		t.Errorf("expected job killed, got %v", job.Status())
	}

	// 后代进程写 marker 的时间点（ready 之后 2 秒）早已过去：文件存在说明
	// 后代进程仍在运行，即 kill 只杀掉了直接子进程。
	time.Sleep(3500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("descendant process is still alive and wrote %s: process tree was not terminated", marker)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker failed: %v", err)
	}
}

// TestServiceFinishedCommandReleasesPipes 验证命令结束后后代进程仍持有管道时
// 任务不会卡死：直接子进程退出（成功）但后代继续持有 stdout 句柄，
// 修复前 Wait 一直等不到 EOF，任务永远停在 running。
func TestServiceFinishedCommandReleasesPipes(t *testing.T) {
	dir := t.TempDir()
	heartbeat := filepath.Join(dir, "heartbeat.txt")
	exited := filepath.Join(dir, "exited.txt")
	stop := filepath.Join(dir, "stop.txt")
	t.Setenv("ALKAID0_HEARTBEAT", heartbeat)
	t.Setenv("ALKAID0_EXITED", exited)
	t.Setenv("ALKAID0_STOP", stop)
	// 无论断言是否失败，退出前都让后代进程自行结束，避免 CI 上残留进程
	defer func() { _ = os.WriteFile(stop, []byte("stop"), 0o644) }()

	// 直接子进程启动后代进程后立即退出（写 exited 文件作为退出时刻标记）；
	// 后代进程持续刷新心跳文件、且不向 stdout 写任何内容，
	// 直到 stop 文件出现——它继承的 stdout 句柄因此在命令结束后依然被持有。
	script := "Start-Process -FilePath powershell " +
		"-ArgumentList '-NoProfile','-Command','while (-not (Test-Path -LiteralPath $env:ALKAID0_STOP)) { Set-Content -LiteralPath $env:ALKAID0_HEARTBEAT -Value (Get-Date -Format o); Start-Sleep -Milliseconds 300 }' " +
		"-NoNewWindow; Set-Content -LiteralPath $env:ALKAID0_EXITED -Value (Get-Date -Format o)"

	job, err := Default.Submit(context.Background(), testRunRequest(script))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	if !waitForFile(t, heartbeat, 60*time.Second) {
		t.Fatalf("后代进程未在 60s 内启动（powershell 不可用？）")
	}
	if !waitForFile(t, exited, 60*time.Second) {
		t.Fatalf("直接子进程未在 60s 内退出")
	}
	exitTime := time.Now()

	// 命令本身必须结束，不能因为后代进程持有管道而卡死
	waitJobDone(t, job, 60*time.Second)

	if job.Status() != JobFinished {
		t.Errorf("expected job finished, got %v", job.Status())
	}
	result := job.Wait(context.Background())
	if result == nil || !result.Success {
		t.Fatalf("expected success result (command exited 0), got %+v", result)
	}

	// 任务在直接子进程退出后至少等了排空上限（DrainTimeout）才结束：
	// 说明这次结束是排空兜底触发的，而不是同步等待后代进程退出。
	if waited := time.Since(exitTime); waited < 1500*time.Millisecond {
		t.Fatalf("job finished %s after the shell exited: 后代进程未持有管道，测试前提不成立", waited)
	}

	// 任务结束后后代进程依然存活（心跳文件继续变化）→ 任务没有等它结束。
	// 注意：后代进程用 Set-Content 写该文件时，Windows 上读取会报"文件被另一进程占用"，
	// 这本身就说明它还在运行，因此读取失败按"仍存活"处理（避免用例偶发失败）。
	first, ferr := os.ReadFile(heartbeat)
	if ferr != nil {
		first = nil // 首次读取失败不影响后续比较
	}
	changed := false
	for range 15 {
		time.Sleep(400 * time.Millisecond)
		cur, rerr := os.ReadFile(heartbeat)
		if rerr != nil {
			changed = true
			break
		}
		if first == nil || string(cur) != string(first) {
			changed = true
			break
		}
	}
	if !changed {
		t.Fatalf("后代进程已不在运行：任务等待了后代进程退出，排空兜底未生效")
	}
}

// TestServiceKillRunProcessTreeTwice 验证重复 kill 幂等且不 panic
// （进程尚未启动时 Kill 读到空的 Process 句柄也不能崩）。
func TestServiceKillRunProcessTreeTwice(t *testing.T) {
	job, err := Default.Submit(context.Background(), testRunRequest("Start-Sleep -Seconds 300"))
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	if err := Default.Kill(job.Workspace, job.ID); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	waitJobDone(t, job, 30*time.Second)

	// 已结束的任务再次 kill 不应 panic/报错
	if err := Default.Kill(job.Workspace, job.ID); err != nil {
		t.Errorf("second Kill returned error: %v", err)
	}
	if job.Status() != JobKilled {
		t.Errorf("expected job killed, got %v", job.Status())
	}
}

// TestServiceInteractiveStdinDoesNotHang 回归：run 工具给每个 shell 请求都设置
// InteractiveStdin=true，修复前 stdin 用 io.Pipe（内存管道）交给命令，必然产生
// 一个阻塞在 Read 上的搬运 goroutine；写入端要等 runCommand 返回后才由 execute
// 关闭，于是命令退出后 Wait 永远等不到它结束，任务停在 running（Windows 无 PTY
// 路径必现）。修复后 stdin 使用 os.Pipe（真实 fd，不需要搬运 goroutine）。
func TestServiceInteractiveStdinDoesNotHang(t *testing.T) {
	req := testRunRequest("Write-Output alkaid0-interactive-stdin")
	req.InteractiveStdin = true
	req.BackgroundKind = "shell"

	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	waitJobDone(t, job, 60*time.Second)

	if job.Status() != JobFinished {
		t.Errorf("expected job finished, got %v", job.Status())
	}
	result := job.Wait(context.Background())
	if result == nil || !result.Success {
		t.Fatalf("expected success result, got %+v", result)
	}
	if !strings.Contains(result.Output, "alkaid0-interactive-stdin") {
		t.Errorf("expected command output, got %q", result.Output)
	}
}
