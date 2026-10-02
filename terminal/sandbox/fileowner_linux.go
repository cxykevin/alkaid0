//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
)

// chownFn 是 os.Chown 的间接层：测试可替换以断言「对哪些路径设置了什么属主」
// （与 tools/tools/tree/ios 的 cloneFileFn 风格一致）。
var chownFn = os.Chown

// resolveFileOwner 解析配置对应的文件属主（Linux：查 /etc/passwd 与 /etc/group）。
// 第二个返回值为非空时表示该配置被忽略（调用方记录一次警告），属主保持当前用户。
func resolveFileOwner(userName, groupName string) (*fileOwner, string) {
	inactive := &fileOwner{uid: -1, gid: -1}
	userName = strings.TrimSpace(userName)
	groupName = strings.TrimSpace(groupName)
	if userName == "" {
		if groupName == "" {
			return inactive, ""
		}
		// 与终端任务一致：用户组是运行用户的补充，未配置运行用户时无从附加
		return inactive, fmt.Sprintf("忽略运行用户组 %q：未配置 Agent.User，编辑文件的属主保持不变", groupName)
	}
	uid, gid, err := lookupOwnerIDs(userName, groupName)
	if err != nil {
		return inactive, fmt.Sprintf("忽略运行用户 %q：%v，编辑文件的属主保持为当前用户", userName, err)
	}
	if uid == os.Geteuid() && gid == os.Getegid() {
		// 与当前进程身份一致：无需 chown（也避免非 root 下必然失败的 chown）
		return inactive, ""
	}
	if os.Geteuid() != 0 {
		return inactive, fmt.Sprintf("无法把编辑文件的属主设置为 %s：需要 root 权限，属主保持为当前用户", ownerLabel(userName, groupName))
	}
	return &fileOwner{uid: uid, gid: gid, active: true}, ""
}

// lookupOwnerIDs 解析目标 uid/gid；groupName 为空时使用用户主组。
func lookupOwnerIDs(userName, groupName string) (int, int, error) {
	u, err := user.Lookup(userName)
	if err != nil {
		return 0, 0, fmt.Errorf("查找用户 %q 失败: %w", userName, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("用户 %q 的 uid %q 无效: %w", userName, u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("用户 %q 的 gid %q 无效: %w", userName, u.Gid, err)
	}
	if groupName != "" {
		_, groupGID, err := resolveGroupID(groupName)
		if err != nil {
			return 0, 0, err
		}
		gid = groupGID
	}
	return uid, gid, nil
}

// ownerLabel 构造日志用的「用户:组」标签。
func ownerLabel(userName, groupName string) string {
	if groupName == "" {
		return userName
	}
	return userName + ":" + groupName
}

// apply 把 path 的属主设置为目标 uid/gid，失败只告警一次。
func (o *fileOwner) apply(path string) {
	if o == nil || !o.active {
		return
	}
	if err := chownFn(path, o.uid, o.gid); err != nil {
		o.warnOnce.Do(func() {
			logger.Warn("设置 %s 的属主为 %d:%d 失败（编辑已完成，属主保持原样）: %v", path, o.uid, o.gid, err)
		})
	}
}
