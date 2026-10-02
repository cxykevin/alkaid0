package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/config"
)

// 文件编辑操作的属主与「终端任务运行用户」保持一致：配置了 Agent.User / Agent.UserGroup 时，
// 工具新建或修改的文件（edit、tree、memory 等）以及新建目录的属主都会设置为目标用户/用户组，
// 避免「以 root 运行的 agent 写出的文件，配置指定的用户改不动」。
//
// 生效条件（Linux，见 fileowner_linux.go）：
//   - 配置了 Agent.User（UserGroup 是其补充，单独配置不生效）；
//   - 目标身份与当前进程身份不一致，且当前进程有权限 chown（通常需要 root）。
//
// 任何不满足条件的情况都只告警一次，绝不阻断编辑操作：属主设置失败与编辑本身无关，
// 调用方也不需要处理错误。

// fileOwner 解析后的目标属主。
// active=false 时 apply 为空操作（未配置 / 与当前身份一致 / 平台不支持 / 解析失败）。
type fileOwner struct {
	uid    int
	gid    int
	active bool
	// warnOnce 保证同一份解析结果只输出一条失败日志（编辑是高频操作，不能每次刷屏）
	warnOnce sync.Once
}

// fileOwnerCache 按配置字符串缓存解析结果：配置不变时只解析/告警一次。
var fileOwnerCache sync.Map // map[string]*fileOwner

// configuredFileOwner 返回当前配置对应的目标属主。
func configuredFileOwner() *fileOwner {
	cfg := config.GlobalConfigSafe()
	return cachedFileOwner(cfg.Agent.User, cfg.Agent.UserGroup)
}

// cachedFileOwner 解析（或复用）user/group 对应的目标属主，解析失败只告警一次。
func cachedFileOwner(userName, groupName string) *fileOwner {
	key := strings.TrimSpace(userName) + "\x00" + strings.TrimSpace(groupName)
	if v, ok := fileOwnerCache.Load(key); ok {
		return v.(*fileOwner)
	}
	owner, warnMsg := resolveFileOwner(userName, groupName)
	if warnMsg != "" {
		logger.Warn("%s", warnMsg)
	}
	fileOwnerCache.Store(key, owner)
	return owner
}

// ApplyConfiguredFileOwner 把 path 的属主设置为配置的运行用户/用户组。
// 未配置、平台不支持、无权限或路径不存在时只记录日志，不影响调用方流程。
func ApplyConfiguredFileOwner(path string) {
	configuredFileOwner().apply(path)
}

// MkdirAllConfigured 等价 os.MkdirAll，并把本次**新建**的各级目录属主设置为配置的目标用户；
// 已存在的目录不做改动。
func MkdirAllConfigured(path string, perm os.FileMode) error {
	missing := missingDirs(path)
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	owner := configuredFileOwner()
	for _, dir := range missing {
		owner.apply(dir)
	}
	return nil
}

// missingDirs 返回 path 及其尚未存在的各级祖先（由浅到深），用于只修正新建目录的属主。
func missingDirs(path string) []string {
	var missing []string
	for p := filepath.Clean(path); ; {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}
