// Package skillpath 统一 skill 的发现根目录与工作区链接目录。
//
// skill 包（发现/启用 skill）与 terminal/sandbox（只读策略）共用这里的路径规则，
// 避免出现「沙盒把某个技能目录当成可写、skill 包却从那里加载」这类不一致。
// 本包不依赖项目内其它包，方便被底层策略直接引用。
package skillpath

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// ManifestName 标准 Agent Skill 的入口文件名（目录内存在该文件即视为 skill）。
	ManifestName = "SKILL.md"
	// InstallDirName 安装目录、配置目录与各 skill 根下存放 skill 的子目录名。
	InstallDirName = "skills"
	// WorkspaceDataDir 工作区内的 alkaid0 数据目录。
	WorkspaceDataDir = ".alkaid0"
	// LinkDirName 工作区内已启用 skill 的链接目录名。
	LinkDirName = "skills"
)

// Skill 根目录来源类型。
const (
	KindWorkspaceClaude = "workspace-claude"
	KindWorkspaceAgents = "workspace-agents"
	KindClaude          = "claude"
	KindAgents          = "agents"
	KindConfig          = "config"
	KindInstall         = "install"
)

// Root 一个 skill 来源根目录。
type Root struct {
	// Path 根目录绝对路径；目录本身可能不存在。
	Path string
	// Kind 来源类型，取值见 Kind* 常量。
	Kind string
}

// Roots 返回 skill 扫描根目录，按优先级从高到低排列。
//
// 顺序即覆盖顺序：同名 skill 以更靠前（更贴近当前工作区）的定义为准。
// workspace 为空时只返回用户级与安装级根目录；configDir 为空时跳过配置目录。
func Roots(workspace, configDir string) []Root {
	var roots []Root
	seen := make(map[string]struct{})
	add := func(path, kind string) {
		if path == "" {
			return
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		path = filepath.Clean(path)
		if path == "" || path == string(filepath.Separator) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		roots = append(roots, Root{Path: path, Kind: kind})
	}

	if workspace != "" {
		add(filepath.Join(workspace, ".claude", InstallDirName), KindWorkspaceClaude)
		add(filepath.Join(workspace, ".agents", InstallDirName), KindWorkspaceAgents)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".claude", InstallDirName), KindClaude)
		add(filepath.Join(home, ".agents", InstallDirName), KindAgents)
	}
	if configDir != "" {
		add(filepath.Join(configDir, InstallDirName), KindConfig)
	}
	for _, dir := range installDirs() {
		add(filepath.Join(dir, InstallDirName), KindInstall)
	}
	return roots
}

// LinkDir 返回工作区内已启用 skill 的链接目录：<workspace>/.alkaid0/skills。
func LinkDir(workspace string) string {
	return filepath.Join(workspace, WorkspaceDataDir, LinkDirName)
}

// Within 判断 target 是否与 root 相同或位于 root 目录内部。
// 两侧都会尽量解析符号链接，root 解析失败时退回词法比较。
func Within(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if real, err := filepath.EvalSymlinks(target); err == nil {
		target = real
	}
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if filesystemEqual(root, target) {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(strings.ToLower(target), strings.ToLower(prefix))
}

// filesystemEqual 比较两个已规范化路径，Windows 下忽略大小写。
func filesystemEqual(a, b string) bool {
	if a == b {
		return true
	}
	return strings.ToLower(a) == strings.ToLower(b)
}

var (
	installDirsOnce sync.Once
	installDirsVal  []string
)

// installDirs 返回可能存放安装目录的目录列表（去重）。
//
// 1) 可执行文件所在目录（`<install>/skills`）；
// 2) Linux 包把静态资源放在 `<prefix>/share/alkaid0`，即 `<exe>/../share/alkaid0`。
func installDirs() []string {
	installDirsOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil || exe == "" {
			return
		}
		if real, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		installDirsVal = append(installDirsVal, dir)
		share := filepath.Join(dir, "..", "share", "alkaid0")
		if abs, absErr := filepath.Abs(share); absErr == nil {
			share = abs
		}
		if share != dir {
			installDirsVal = append(installDirsVal, share)
		}
	})
	return installDirsVal
}
