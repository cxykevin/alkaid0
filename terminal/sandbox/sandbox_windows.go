//go:build windows

package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	winSandbox "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows"
	"golang.org/x/sys/windows"
)

type windowsCommandCleanup struct {
	token   *windows.Token
	release func() error
}

// Clean 调用 release 还原目录权限/ACL（由 Command.Wait 统一 defer 调用）
func (cl *windowsCommandCleanup) Clean() error {
	if cl.release != nil {
		return cl.release()
	}
	return nil
}

func (s *Sandbox) createIsolatedCommand(ctx context.Context, name string, args ...string) (*Command, error) {
	if err := winSandbox.InitAlkaid0SandboxUser(); err != nil {
		return nil, fmt.Errorf("初始化沙盒用户失败: %w", err)
	}

	// 统一策略落地：与 Linux 沙盒读同一份 terminal/sandbox.Policy。
	//   - 可读写路径 → 追加继承性"完全控制"ACE；
	//   - 配置/日志目录 → 追加继承性拒绝 ACE（内容完全隐藏）；
	//   - 工作区 .alkaid0 → 目录可列不可写，子项逐个拒绝读写（白名单除外）。
	// 所有被改动的 DACL 都保存快照，命令结束后按逆序还原。
	winPolicy := winSandbox.Policy{}
	if s.policy != nil {
		winPolicy.ReadWrite = s.policy.Paths(AccessReadWrite)
		winPolicy.Deny = s.policy.Paths(AccessDeny)
		winPolicy.ReadOnly = s.policy.Paths(AccessReadOnly)
		winPolicy.ListOnly = s.policy.Paths(AccessListOnly)
		winPolicy.AllowedChildren = s.policy.AllowedChildren()
	} else {
		// 策略缺失（理论上不会发生）：退回旧的可写目录白名单
		winPolicy.ReadWrite = s.writableDirs
	}
	release, err := winSandbox.ApplyPolicy(winPolicy)
	if err != nil {
		return nil, fmt.Errorf("设置沙盒路径权限失败: %w", err)
	}

	cmd := winSandbox.CommandContext(ctx, name, args...)
	cmd.Dir = s.workDir
	cmd.Env = s.env
	if resolved, err := exec.LookPath(name); err == nil {
		cmd.Path = resolved
	} else if cmd.Path == "" {
		cmd.Path = name
	}

	return &Command{
		cmd:     cmd,
		ctx:     ctx,
		name:    name,
		args:    args,
		workDir: s.workDir,
		env:     s.env,
		temp: &windowsCommandCleanup{
			token: nil,
			// ApplyPolicy 内部按逆序还原（子项先于父目录），这里直接复用。
			release: release,
		},
	}, nil
}

// sameWindowsPath 判断两个路径是否指向同一个目录。
// Windows 路径大小写不敏感，且可能带尾部分隔符、混用 / 与 \，
// 因此先 filepath.Clean（在 Windows 上会把分隔符统一为 \ 并去掉尾部斜杠）再忽略大小写比较。
func sameWindowsPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
