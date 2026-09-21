package sandbox

import (
	"context"
	"errors"
	"io"
	"testing"
)

// startFailCmd 模拟 Start 永远失败的底层命令（进程未创建：PID 为 0）。
type startFailCmd struct{}

func (f *startFailCmd) Start() error        { return errors.New("exec: fake start failure") }
func (f *startFailCmd) Wait() error         { return nil }
func (f *startFailCmd) Kill() error         { return nil }
func (f *startFailCmd) SetStdin(io.Reader)  {}
func (f *startFailCmd) SetStdout(io.Writer) {}
func (f *startFailCmd) SetStderr(io.Writer) {}
func (f *startFailCmd) Clean()              {}

// alreadyStartedCmd 模拟"进程已在运行、重复 Start 失败"的底层命令（PID 非 0）。
type alreadyStartedCmd struct{ startFailCmd }

func (f *alreadyStartedCmd) Start() error { return errors.New("exec: already started") }
func (f *alreadyStartedCmd) PID() int     { return 12345 }

// cleanupRecorder 记录 Clean 是否被调用（对应 Windows 沙盒的目录 ACL 还原）。
type cleanupRecorder struct{ called int }

func (r *cleanupRecorder) Clean() error { r.called++; return nil }

// TestCommandStartFailureRunsCleanup 回归测试：Start 失败时进程并未创建，
// 调用方不会再调用 Wait（Wait 是 Clean 的常规调用点），必须在此刻立即清理
// 临时资源；否则 Windows 沙盒为目录授予的 ACL 会永久残留。
func TestCommandStartFailureRunsCleanup(t *testing.T) {
	rec := &cleanupRecorder{}
	c := &Command{cmd: &startFailCmd{}, ctx: context.Background(), name: "fake", temp: rec}

	if err := c.Start(); err == nil {
		t.Fatal("Start 应返回错误")
	}
	if rec.called != 1 {
		t.Fatalf("Start 失败后必须调用一次 Clean，实际 %d 次", rec.called)
	}
}

// TestCommandStartAlreadyStartedKeepsCleanup 回归测试：进程已启动时重复 Start
// 返回失败，此时不能清理正在使用的资源（Clean 只在进程未创建时触发）。
func TestCommandStartAlreadyStartedKeepsCleanup(t *testing.T) {
	rec := &cleanupRecorder{}
	c := &Command{cmd: &alreadyStartedCmd{}, ctx: context.Background(), name: "fake", temp: rec}

	if err := c.Start(); err == nil {
		t.Fatal("Start 应返回错误")
	}
	if rec.called != 0 {
		t.Fatalf("已启动进程的重复 Start 失败不得调用 Clean，实际 %d 次", rec.called)
	}
}
