//go:build !linux && !darwin && !windows

package sandbox

import (
	"fmt"
	"os/user"
	"strings"
)

// resolveRunAsUser 解析配置指定的运行用户（当前平台暂不支持切换，仅用于给出提示）。
// 用户组（Agent.UserGroup）仅 Linux 生效：这里只记录，由 prepareRunAs 给出提示。
func resolveRunAsUser(name, group string) (*runAsUser, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("查找用户 %q 失败: %w", name, err)
	}
	return &runAsUser{Name: u.Username, Home: u.HomeDir, Group: strings.TrimSpace(group)}, nil
}

// prepareRunAs 当前平台暂不支持以指定用户运行命令：保持现状并告警。
func prepareRunAs(ru *runAsUser) (bool, string) {
	if ru.Group != "" {
		return false, "用户组（Agent.UserGroup）与运行用户均仅在 Linux 生效，当前平台不支持"
	}
	return false, "当前平台暂不支持指定终端任务的运行用户"
}

// wrapIsolateNoneCommand 当前平台不会走到用户切换（见 prepareRunAs），原样返回。
func wrapIsolateNoneCommand(s *Sandbox, name string, args []string) (string, []string) {
	return name, args
}
