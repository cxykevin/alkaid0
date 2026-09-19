package u

import (
	"fmt"
	"path/filepath"
	"strings"
)

// EnsureWorkspacePath 校验 target 位于 root 之内，且路径中不出现被保护的分量
// （典型为 ".alkaid0"，会话数据库所在目录）。
//
// 为什么需要它：各工具（read/edit/search）此前的路径校验都是**纯词法**的
// （拒绝 ".."、绝对路径、通配符），这挡不住工作区内的一个符号链接——
// os.ReadFile / os.WriteFile / os.Stat 都会跟随链接，于是"相对路径"可以
// 读写工作区之外的文件。本函数做两件事：
//
//  1. 词法分量检查（不依赖文件是否存在，含新建文件的场景）；
//  2. 沿 target 向上取最长的**已存在**前缀做 filepath.EvalSymlinks 解析，
//     确认解析结果仍落在 root 之内，并对解析后的相对路径再查一次被保护分量
//     （防止 root 内的链接指向 .alkaid0 之类的受保护目录）。
//
// 返回 nil 表示安全；否则返回描述性错误。
func EnsureWorkspacePath(root, target string, deniedComponents ...string) error {
	if root == "" || target == "" {
		return fmt.Errorf("empty path")
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = filepath.Clean(root)
	}
	cleanTarget := filepath.Clean(target)

	// 1) 词法检查
	if rel, relErr := filepath.Rel(realRoot, cleanTarget); relErr == nil {
		if err := checkDeniedComponents(rel, deniedComponents); err != nil {
			return err
		}
	}

	// 2) 跟随符号链接后的检查
	check := cleanTarget
	for {
		resolved, err := filepath.EvalSymlinks(check)
		if err == nil {
			rel, relErr := filepath.Rel(realRoot, resolved)
			if relErr != nil || escapesRoot(rel) {
				return fmt.Errorf("path escapes the workspace via symlink: %s", target)
			}
			return checkDeniedComponents(rel, deniedComponents)
		}
		parent := filepath.Dir(check)
		if parent == check {
			// 已回溯到文件系统根：target 及其祖先都不存在，
			// 没有任何已存在的链接可被跟随，按安全处理。
			return nil
		}
		check = parent
	}
}

// escapesRoot 判断相对路径是否指向 root 之外。
func escapesRoot(rel string) bool {
	if rel == ".." {
		return true
	}
	return strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkDeniedComponents 检查相对路径中是否出现被保护的分量。
func checkDeniedComponents(rel string, denied []string) error {
	if len(denied) == 0 || rel == "" || rel == "." {
		return nil
	}
	parts := strings.FieldsFunc(rel, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, part := range parts {
		for _, d := range denied {
			if d != "" && strings.EqualFold(part, d) {
				return fmt.Errorf("access to %s directory is not allowed", d)
			}
		}
	}
	return nil
}
