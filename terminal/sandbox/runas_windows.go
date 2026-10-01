//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	winSandbox "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows"
)

// resolveRunAsUser 解析配置指定的运行用户（Windows）。
//
// Windows 上以其它用户启动进程必须有该用户的凭据（LogonUser/CreateProcessWithLogonW）
// 或 SYSTEM 权限的令牌，本项目暂不收集凭据，因此这里只做解析与提示。
func resolveRunAsUser(name string) (*runAsUser, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, fmt.Errorf("用户名为空")
	}
	ru := &runAsUser{Name: trimmed}
	if cur, err := user.Current(); err == nil && sameWindowsAccount(cur.Username, trimmed) {
		ru.Name = cur.Username
		ru.sameAsCurrent = true
	}
	return ru, nil
}

// prepareRunAs Windows 上仅"配置与当前用户一致"可以静默忽略；
// 其它用户一律回退当前用户并给出告警（沙盒内本就用沙盒账户运行）。
func prepareRunAs(ru *runAsUser) (bool, string) {
	if ru.sameAsCurrent {
		return false, ""
	}
	if sameWindowsAccount(ru.Name, winSandbox.UserName) {
		return false, "Windows 沙盒内的命令本就在沙盒账户下运行，沙盒外切换该账户暂不支持"
	}
	return false, "Windows 上以其它用户运行命令需要该用户的凭据（密码），暂不支持"
}

// wrapIsolateNoneCommand Windows 上不会走到用户切换（见 prepareRunAs），原样返回。
func wrapIsolateNoneCommand(s *Sandbox, name string, args []string) (string, []string) {
	return name, args
}

// splitWindowsAccount 把账户名拆成（域, 用户名）。
//
// 缺省域（"administrator"）与 "." 都表示本机账户，统一归一化为当前计算机名——
// 否则「其它域\administrator」会因为域被当成可忽略而被误判为当前用户。
func splitWindowsAccount(s string) (string, string) {
	s = strings.TrimSpace(s)
	domain := ""
	if i := strings.LastIndexByte(s, '\\'); i >= 0 {
		domain, s = s[:i], s[i+1:]
	}
	if domain == "" || domain == "." {
		domain = localWindowsHostName()
	}
	return domain, s
}

// localWindowsHostName 返回当前计算机名，用于把缺省/「.」域归一化为本机账户。
// 取不到计算机名时退化为 "."，保证同一进程内的比较仍然自洽。
func localWindowsHostName() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "."
}

// sameWindowsAccount 比较两个 Windows 账户名是否指向同一账户。
//
// 用户名与域名均不区分大小写；缺省域按本机解析（与 Windows 的账户解析语义一致），
// 因此 "administrator"、"计算机名\administrator"、".\administrator" 等价，
// 而 "其它域\administrator" 不等价——否则它会被当成当前用户而静默忽略，
// 用户会误以为配置的运行用户已经生效。
func sameWindowsAccount(a, b string) bool {
	domainA, nameA := splitWindowsAccount(a)
	domainB, nameB := splitWindowsAccount(b)
	return strings.EqualFold(nameA, nameB) && strings.EqualFold(domainA, domainB)
}
