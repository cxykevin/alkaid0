//go:build !linux && !darwin && !windows

package sandbox

import (
	"fmt"
	"os/user"
)

// resolveRunAsUser 解析配置指定的运行用户（当前平台暂不支持切换，仅用于给出提示）。
func resolveRunAsUser(name string) (*runAsUser, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("查找用户 %q 失败: %w", name, err)
	}
	return &runAsUser{Name: u.Username, Home: u.HomeDir}, nil
}

// prepareRunAs 当前平台暂不支持以指定用户运行命令：保持现状并告警。
func prepareRunAs(ru *runAsUser) (bool, string) {
	return false, "当前平台暂不支持指定终端任务的运行用户"
}

// wrapIsolateNoneCommand 当前平台不会走到用户切换（见 prepareRunAs），原样返回。
func wrapIsolateNoneCommand(s *Sandbox, name string, args []string) (string, []string) {
	return name, args
}
