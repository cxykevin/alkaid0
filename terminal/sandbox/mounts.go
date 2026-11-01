package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// 工作区挂载表。
//
// file 工具的 type="mount" 会在工作区**根目录**创建 <dst>.alkmount 软链接，
// 指向一个工作区之外的绝对目录，并把目标目录登记到工作区根的
// .alkaid0/mounts.json。沙盒构建访问策略时读取这张表，把目标目录加入可读写
// 白名单——否则挂载点只有软链接、目标目录仍处于"全盘只读"，挂载就只是个能看
// 不能写的指针。
//
// 链接固定放在工作区根：沙盒豁免按工作区整体生效，链接也应在整棵工作区可见
// （子激活路径、根目录都能用），回收与自愈（tree 扫描）也以同一位置为准。
const (
	// mountTableFile 挂载表相对工作区根的路径。
	mountTableFile = ".alkaid0/mounts.json"
	// MountLinkSuffix 挂载软链接的固定后缀：dst 只允许字母、数字与下划线，
	// 实际创建的链接名为 dst + MountLinkSuffix。
	MountLinkSuffix = ".alkmount"
)

// mountNamePattern 挂载名（dst）允许的字符集。
var mountNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// mountLinkNamePattern 挂载链接的完整文件名（name + 固定后缀）。
var mountLinkNamePattern = regexp.MustCompile(`^([A-Za-z0-9_]+)\.alkmount$`)

// mountTableMu 同进程内串行化挂载表写入（读-改-写）与自愈清理。
var mountTableMu sync.Mutex

// MountEntry 一条挂载记录：Name 是挂载名（链接去掉后缀的部分），
// Target 是被挂载的绝对目录。
type MountEntry struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// mountTable 挂载表的磁盘格式。
type mountTable struct {
	Mounts []MountEntry `json:"mounts"`
}

// ValidMountName 报告 name 是否是合法的挂载名（仅字母、数字、下划线）。
func ValidMountName(name string) bool {
	return mountNamePattern.MatchString(name)
}

// MountLinkName 返回挂载名对应的链接文件名（name + MountLinkSuffix）。
func MountLinkName(name string) string {
	return name + MountLinkSuffix
}

// MountLinkPath 返回挂载名对应的链接绝对路径（工作区根下）。
func MountLinkPath(workspace, name string) string {
	return filepath.Join(workspace, MountLinkName(name))
}

// MountNameFromLink 从链接文件名（或路径的 basename）解析挂载名。
// 名字不合法（例如 data.bak.alkmount）时返回 false。
func MountNameFromLink(name string) (string, bool) {
	m := mountLinkNamePattern.FindStringSubmatch(filepath.Base(name))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// MountTablePath 返回工作区挂载表的绝对路径。
func MountTablePath(workspace string) string {
	return filepath.Join(workspace, filepath.FromSlash(mountTableFile))
}

// LoadMounts 读取工作区的挂载表。表不存在或内容损坏时返回 nil（沙盒与工具
// 都不应因为一张坏表而无法工作）。
func LoadMounts(workspace string) []MountEntry {
	entries, err := readMountTable(workspace)
	if err != nil && !os.IsNotExist(err) {
		logger.Warn("sandbox: failed to read mount table %s: %v", MountTablePath(workspace), err)
	}
	return entries
}

// MountTargets 返回挂载表中当前仍存在的目标目录（已规范化并去重），
// 供策略构建时并入可写白名单。目标被删除的条目直接跳过。
func MountTargets(workspace string) []string {
	entries := LoadMounts(workspace)
	if len(entries) == 0 {
		return nil
	}
	targets := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		target := canonicalPath(entry.Target)
		if _, dup := seen[target]; dup {
			continue
		}
		info, err := os.Stat(target)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	return targets
}

// AddMount 登记（或更新）一条挂载记录：同名条目被替换为新的目标目录。
// 目标必须是已存在的绝对目录；写表采用临时文件 + rename，避免半截 JSON。
func AddMount(workspace, name, target string) error {
	if !ValidMountName(name) {
		return fmt.Errorf("invalid mount name %q: only letters, digits and underscore are allowed", name)
	}
	if !filepath.IsAbs(target) {
		return fmt.Errorf("mount target must be an absolute directory: %s", target)
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("mount target not found: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("mount target is not a directory: %s", target)
	}
	cleanTarget := canonicalPath(target)

	mountTableMu.Lock()
	defer mountTableMu.Unlock()

	entries, err := readMountTable(workspace)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read mount table: %w", err)
	}
	replaced := false
	for i := range entries {
		if entries[i].Name == name {
			entries[i].Target = cleanTarget
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, MountEntry{Name: name, Target: cleanTarget})
	}
	return writeMountTable(workspace, entries)
}

// RemoveMount 从挂载表移除指定名字的记录，返回是否真的删掉了一条。
// 供 move 到 @trash 回收挂载时使用；调用方负责删除软链接本身。
func RemoveMount(workspace, name string) bool {
	if !ValidMountName(name) {
		return false
	}
	mountTableMu.Lock()
	defer mountTableMu.Unlock()

	entries, err := readMountTable(workspace)
	if err != nil || len(entries) == 0 {
		return false
	}
	kept := make([]MountEntry, 0, len(entries))
	removed := false
	for _, entry := range entries {
		if entry.Name == name {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return false
	}
	if err := writeMountTable(workspace, kept); err != nil {
		logger.Warn("sandbox: failed to remove mount %q: %v", name, err)
		return false
	}
	return true
}

// ReconcileMounts 清理挂载表中已失效的条目：链接不存在、不是符号链接、
// 指向的目标与记录不一致、或目标目录不可达时，删除链接并把该条目移出表。
// 返回清理掉的条目数。
//
// 只做"删除失效项"：工作区里出现一个未知的 *.alkmount 链接**不会**被自动
// 登记（新增挂载必须由 AI 显式调用 file 工具的 mount 操作），避免扫描行为
// 意外把任意目录并入沙盒可写白名单。
func ReconcileMounts(workspace string) int {
	mountTableMu.Lock()
	defer mountTableMu.Unlock()

	entries, err := readMountTable(workspace)
	if err != nil || len(entries) == 0 {
		return 0
	}
	kept := make([]MountEntry, 0, len(entries))
	removed := 0
	for _, entry := range entries {
		if mountLinkValid(workspace, entry) {
			kept = append(kept, entry)
			continue
		}
		removed++
		link := MountLinkPath(workspace, entry.Name)
		if info, statErr := os.Lstat(link); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			if rmErr := os.Remove(link); rmErr != nil {
				logger.Warn("sandbox: failed to remove stale mount link %s: %v", link, rmErr)
			}
		}
		logger.Info("sandbox: dropped stale mount %q (target %s)", entry.Name, entry.Target)
	}
	if removed == 0 {
		return 0
	}
	if err := writeMountTable(workspace, kept); err != nil {
		logger.Warn("sandbox: failed to rewrite mount table after reconcile: %v", err)
	}
	return removed
}

// mountLinkValid 报告一条挂载记录是否仍然有效：链接存在且是符号链接、
// 指向表里记录的目标目录、目标目录可访问。
func mountLinkValid(workspace string, entry MountEntry) bool {
	link := MountLinkPath(workspace, entry.Name)
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	resolved, err := os.Readlink(link)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(link), resolved)
	}
	if canonicalPath(resolved) != canonicalPath(entry.Target) {
		return false
	}
	targetInfo, err := os.Stat(entry.Target)
	if err != nil || !targetInfo.IsDir() {
		return false
	}
	return true
}

// writeMountTable 原子写回挂载表（调用方必须持有 mountTableMu）。
func writeMountTable(workspace string, entries []MountEntry) error {
	if entries == nil {
		entries = []MountEntry{}
	}
	data, err := json.MarshalIndent(mountTable{Mounts: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode mount table: %w", err)
	}
	data = append(data, '\n')

	path := MountTablePath(workspace)
	if err := MkdirAllConfigured(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create mount table directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mounts-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temporary mount table: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write mount table: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write mount table: %w", err)
	}
	ApplyConfiguredFileOwner(tmpName)
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to replace mount table: %w", err)
	}
	return nil
}

// readMountTable 读取并解析挂载表；文件不存在时返回 os.ErrNotExist。
func readMountTable(workspace string) ([]MountEntry, error) {
	data, err := os.ReadFile(MountTablePath(workspace))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var table mountTable
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, err
	}
	return table.Mounts, nil
}
