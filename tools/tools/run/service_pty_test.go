//go:build !windows

package run

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestBackgroundJobFinishesWhenDescendantHoldsPTY 回归：命令自身已退出，但留下的
// 后代进程仍持有 PTY 从端（例如把 dev server 放到后台）时，任务必须结束，
// 不能停在 running。
//
// 背景：PTY master 若以阻塞方式打开，Close 无法唤醒阻塞中的 read（Linux/darwin），
// 命令结束后的排空等待（copyWg.Wait）会永远卡住——进程侧早已结束，任务却一直
// 停在 running。修复：master 以 O_NONBLOCK 打开并交给 runtime poller 管理
// （见 terminal/pty），Close 可取消挂起读；排空后仍保留上限兜底。
func TestBackgroundJobFinishesWhenDescendantHoldsPTY(t *testing.T) {
	// 后台驻留进程：命令自身立刻退出，它继续持有 PTY 从端。
	// 用不易撞车的秒数，便于结束测试后清理。
	const lingerSeconds = "31415"
	req := testRunRequest("sleep " + lingerSeconds + " &")
	req.BackgroundKind = "shell"

	job, err := Default.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}
	t.Cleanup(func() {
		_ = Default.Kill(job.Workspace, job.ID)
		_ = exec.Command("pkill", "-f", "sleep "+lingerSeconds).Run()
	})

	select {
	case <-job.Done():
	case <-time.After(30 * time.Second):
		t.Fatalf("job %s stuck in running after the shell exited (descendant holds the pty)", job.ID)
	}

	if job.Status() != JobFinished {
		t.Errorf("expected job finished, got %v", job.Status())
	}
	if result := job.Wait(context.Background()); result == nil {
		t.Error("expected non-nil result")
	}
}
