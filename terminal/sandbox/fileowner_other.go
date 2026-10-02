//go:build !linux

package sandbox

import (
	"fmt"
	"strings"
)

// resolveFileOwner 非 Linux 平台不支持设置文件属主（用户组同样仅 Linux 生效）：
// 配置了就在首次使用时告警一次，编辑行为保持不变。
func resolveFileOwner(userName, groupName string) (*fileOwner, string) {
	inactive := &fileOwner{uid: -1, gid: -1}
	userName = strings.TrimSpace(userName)
	groupName = strings.TrimSpace(groupName)
	if userName == "" && groupName == "" {
		return inactive, ""
	}
	return inactive, fmt.Sprintf("编辑文件的属主设置仅在 Linux 生效，已忽略（Agent.User=%q, Agent.UserGroup=%q）", userName, groupName)
}

// apply 在非 Linux 平台为空操作（见 resolveFileOwner）。
func (o *fileOwner) apply(string) {}
