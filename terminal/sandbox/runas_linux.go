//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
)

// resolveRunAsUser 解析配置指定的运行用户与用户组（Linux：查 /etc/passwd 与 /etc/group）。
//
// group 为空时使用用户的主组；非空时按组名（或数字 gid）解析，因此 GID 可能与主组不同。
func resolveRunAsUser(name, group string) (*runAsUser, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("查找用户 %q 失败: %w", name, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return nil, fmt.Errorf("用户 %q 的 uid %q 无效: %w", name, u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, fmt.Errorf("用户 %q 的 gid %q 无效: %w", name, u.Gid, err)
	}
	ru := &runAsUser{
		Name: u.Username,
		UID:  uid,
		GID:  gid,
		Home: u.HomeDir,
	}
	if group != "" {
		groupName, groupGID, err := resolveGroupID(group)
		if err != nil {
			return nil, err
		}
		ru.Group = groupName
		ru.GID = groupGID
	}
	// 身份完全一致（uid + gid）才算"就是当前用户"：只换组同样需要切换权限
	ru.sameAsCurrent = ru.UID == os.Geteuid() && ru.GID == os.Getegid()
	return ru, nil
}

// resolveGroupID 解析用户组：先按组名查，失败再按数字 gid 查（与 user.Lookup 接受数字 uid 的行为对齐）。
func resolveGroupID(group string) (string, int, error) {
	g, err := user.LookupGroup(group)
	if err != nil {
		if byID, idErr := user.LookupGroupId(group); idErr == nil {
			g, err = byID, nil
		}
	}
	if err != nil {
		return "", 0, fmt.Errorf("查找用户组 %q 失败: %w", group, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return "", 0, fmt.Errorf("用户组 %q 的 gid %q 无效: %w", group, g.Gid, err)
	}
	return g.Name, gid, nil
}

// prepareRunAs 判断当前进程是否有能力以指定用户运行命令。
//
// 返回 use=false 且 reason 非空时由调用方记录一次警告并回退当前用户；
// 目标用户就是当前用户（reason 为空）时静默忽略——无需切换。
func prepareRunAs(ru *runAsUser) (bool, string) {
	if ru.sameAsCurrent {
		return false, ""
	}
	if os.Geteuid() != 0 {
		return false, "需要 root 权限才能以其它用户运行命令"
	}
	if _, err := exec.LookPath("setpriv"); err != nil {
		return false, "未找到 setpriv（util-linux），无法切换用户"
	}
	return true, ""
}

// wrapIsolateNoneCommand 在沙盒外（IsolationNone）把命令包装成以目标用户运行。
//
// 使用 setpriv 而非 su：su 会重置 HOME/SHELL/USER 等环境变量并可能要求 tty，
// 而 setpriv 只改 uid/gid 后 exec，命令仍是同一个进程、环境与 PTY 句柄都保持不变
// （与 mount.sh 沙盒内的降权方式一致）。
func wrapIsolateNoneCommand(s *Sandbox, name string, args []string) (string, []string) {
	ru := s.runAs
	if ru == nil || ru.sameAsCurrent || os.Geteuid() == ru.UID {
		return name, args
	}
	setpriv, err := exec.LookPath("setpriv")
	if err != nil {
		logger.Warn("setpriv 不可用，无法以用户 %s 运行命令，回退当前用户: %v", ru.Name, err)
		return name, args
	}
	wrapped := make([]string, 0, len(args)+5)
	wrapped = append(wrapped,
		"--reuid="+strconv.Itoa(ru.UID),
		"--regid="+strconv.Itoa(ru.GID),
		"--clear-groups",
		"--",
		name,
	)
	return setpriv, append(wrapped, args...)
}

// sandboxRunOwner 返回 Linux 沙盒内降权使用的 uid/gid/用户名。
// 优先使用配置指定的用户；未配置时回落工作目录实际属主（原有行为）。
func (s *Sandbox) sandboxRunOwner() (int, int, string) {
	if s.runAs != nil {
		return s.runAs.UID, s.runAs.GID, s.runAs.Name
	}
	uid, gid := s.getWorkDirOwner()
	name := "user"
	if uid != 0 {
		if u, err := user.LookupId(strconv.Itoa(uid)); err == nil && u.Username != "" {
			name = u.Username
		}
	}
	return uid, gid, name
}
