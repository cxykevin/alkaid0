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

	// workDir 必须从 SetLimitToDir 的列表中排除，交给 SetLimitToWorkdir 单独处理。
	// 原因有两个，缺一都会让权限无法还原：
	//  1) 两个函数都会保存各自目录的原始 DACL 快照。SetLimitToWorkdir 读取的是
	//     "当前" DACL —— 如果 SetLimitToDir 已经先改过 workDir，它保存到的就是
	//     "已授予沙盒账户"的那份，清理时会把这份授予权限当成原始值写回去。
	//     先排除 workDir，SetLimitToWorkdir 才能快照到真正的原始 DACL。
	//  2) SetLimitToWorkdir 是 SetLimitToDir 对 workDir 的超集（除了授予写权限，
	//     还会给 <workDir>/.alkaid0 施加沙盒账户的 DenyDACL）；两者都清理 workDir
	//     时，后执行的那个会覆盖前一个的结果。
	otherDirs := make([]string, 0, len(s.writableDirs))
	for _, dir := range s.writableDirs {
		if !sameWindowsPath(dir, s.workDir) {
			otherDirs = append(otherDirs, dir)
		}
	}

	release1, err := winSandbox.SetLimitToDir(otherDirs)
	if err != nil {
		// SetLimitToDir 可能已经成功改过列表里靠前的若干目录后才失败；它返回的
		// 清理函数正好用于回滚这些已改动项。不调用就会把"已授予沙盒账户"的
		// DACL 永久留在那些目录上。
		if release1 != nil {
			_ = release1()
		}
		return nil, fmt.Errorf("设置目录权限失败: %w", err)
	}

	release2, err := winSandbox.SetLimitToWorkdir(s.workDir)
	if err != nil {
		// 同理：SetLimitToWorkdir 可能已经给 workDir 授予了权限（仅在 .alkaid0
		// 那几步失败时才会带着错误返回），必须回滚，否则沙盒账户会永久保留
		// 对工作目录的写权限。
		if release2 != nil {
			_ = release2()
		}
		_ = release1()
		return nil, fmt.Errorf("设置工作目录权限失败: %w", err)
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
			release: func() error {
				// 按相反顺序释放：先恢复 workDir，再恢复其余目录。
				// 两组目录不会重叠（workDir 已被排除），而恢复 workDir 会重新触发
				// 继承传播、可能覆盖其未受保护子目录的 DACL —— 把子目录的恢复放在
				// 后面执行，最终状态才一定是各自保存的原始值。
				if err := release2(); err != nil {
					return err
				}
				return release1()
			},
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
