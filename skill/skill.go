// Package skill 发现并启用 Agent Skills。
//
// 扫描 alkaid0 安装目录、配置目录、用户/工作区的 .claude 与 .agents 目录下的
// 标准 skill（子目录内含 SKILL.md），把每个 skill 以 skill.<name> 的形式暴露为
// 工具 scope。启用 scope 时把 skill 目录软链接到
// <workspace>/.alkaid0/skills/<name>，停用时删除该链接。
//
// 软链接只指向发现的 skill 目录：停用只删除软链接本身，绝不删除真实目录；
// 沙盒策略把 skill 来源目录与 .alkaid0/skills 都收敛为只读。
package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/internal/skillpath"
	"github.com/cxykevin/alkaid0/log"
	"gopkg.in/yaml.v3"
)

// ScopePrefix skill 专用 scope 前缀，最终名字形如 skill.pdf-processing。
const ScopePrefix = "skill."

// maxManifestSize 单个 SKILL.md 的读取上限（防止把超大文件当 skill 读进内存）。
const maxManifestSize = 1 << 20

// maxDescriptionRunes scope 描述的长度上限，避免把整段正文塞进系统提示词。
const maxDescriptionRunes = 500

var logger = log.New("skill")

// Skill 一个被发现的 skill。
type Skill struct {
	// Name 规范化后的名称（小写，仅含字母/数字与 - _ .），同时用作 scope 后缀与链接名。
	Name string
	// Description 来自 frontmatter 的一行描述；缺失时取正文首个非空行。
	Description string
	// Dir skill 目录的绝对路径。
	Dir string
	// Source 来源根类型，取值见 skillpath.Kind*。
	Source string
}

// Scope 返回该 skill 对应的 scope 名：skill.<name>。
func (s Skill) Scope() string {
	return ScopePrefix + s.Name
}

// WorkspaceDir 返回 skill 名称在工作区内的相对链接目录：.alkaid0/skills/<name>。
// 统一用 "/" 作为分隔符，便于与工具路径（模型输入的相对路径）一致。
func WorkspaceDir(name string) string {
	return filepath.ToSlash(filepath.Join(skillpath.WorkspaceDataDir, skillpath.LinkDirName, name))
}

// WorkspaceManifest 返回 skill 在工作区内的相对入口文件路径：
// .alkaid0/skills/<name>/SKILL.md。
func WorkspaceManifest(name string) string {
	return filepath.ToSlash(filepath.Join(skillpath.WorkspaceDataDir, skillpath.LinkDirName, name, skillpath.ManifestName))
}

// WorkspaceManifest 返回该 skill 在工作区内的相对入口文件路径。
func (s Skill) WorkspaceManifest() string {
	return WorkspaceManifest(s.Name)
}

// ScopeItem 渲染 <scopes> 段所需的条目。
type ScopeItem struct {
	ID          string
	Description string
}

// IsSkillScope 判断 scope 名是否为 skill scope。
func IsSkillScope(name string) bool {
	return strings.HasPrefix(name, ScopePrefix) && len(name) > len(ScopePrefix)
}

// Alkaid0ConfigDir 返回 alkaid0 配置/数据目录的绝对路径；无法确定时返回空串。
func Alkaid0ConfigDir() string {
	p := config.Path()
	if p == "" {
		return ""
	}
	dir := filepath.Dir(configutil.ExpandPath(p))
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return dir
}

// Discover 扫描所有 skill 根目录，返回去重后的 skill 列表（按名称排序）。
//
// 同名 skill 以更靠前的根目录为准（工作区 > 用户 > 配置 > 安装）。
func Discover(workspace string) []Skill {
	var out []Skill
	seen := make(map[string]struct{})
	for _, root := range skillpath.Roots(workspace, Alkaid0ConfigDir()) {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			dir := filepath.Join(root.Path, entry.Name())
			info, err := os.Stat(dir) // 跟随符号链接：允许把别处的 skill 链接进根目录
			if err != nil || !info.IsDir() {
				continue
			}
			sk, ok := load(dir, root.Kind)
			if !ok {
				continue
			}
			if _, exists := seen[sk.Name]; exists {
				continue
			}
			seen[sk.Name] = struct{}{}
			out = append(out, sk)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Scopes 返回工作区可用的 skill scope 列表（已按 ID 排序）。
func Scopes(workspace string) []ScopeItem {
	skills := Discover(workspace)
	items := make([]ScopeItem, 0, len(skills))
	for _, s := range skills {
		items = append(items, ScopeItem{ID: s.Scope(), Description: s.Description})
	}
	return items
}

// Lookup 在当前工作区查找 skill；scope 可带也可不带 "skill." 前缀。
func Lookup(workspace, scope string) (Skill, bool) {
	name := strings.TrimPrefix(scope, ScopePrefix)
	for _, s := range Discover(workspace) {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// Enable 把 skill 目录软链接到 <workspace>/.alkaid0/skills/<name>。
// 已存在指向同一 skill 的链接时视为成功；指向别处的旧链接会被替换；
// 若非软链接（真实文件/目录）则报错，避免误删用户内容。
func Enable(workspace string, s Skill) error {
	if workspace == "" {
		return errors.New("workspace is empty")
	}
	if s.Name == "" || s.Dir == "" {
		return errors.New("invalid skill")
	}
	linkDir := skillpath.LinkDir(workspace)
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}
	link := filepath.Join(linkDir, s.Name)
	info, err := os.Lstat(link)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("skill link %q already exists and is not a symlink", link)
		}
		if sameTarget(link, s.Dir) {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("replace stale skill link: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("inspect skill link: %w", err)
	}
	if err := os.Symlink(s.Dir, link); err != nil {
		return fmt.Errorf("create skill symlink: %w", err)
	}
	logger.Info("enabled skill %q -> %s", s.Name, s.Dir)
	return nil
}

// Disable 删除 <workspace>/.alkaid0/skills/<name> 软链接。
// 路径不存在视为成功；非软链接一律拒绝删除。
func Disable(workspace, name string) error {
	if workspace == "" {
		return errors.New("workspace is empty")
	}
	link := filepath.Join(skillpath.LinkDir(workspace), name)
	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect skill link: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refusing to remove %q: not a skill symlink", link)
	}
	if err := os.Remove(link); err != nil {
		return fmt.Errorf("remove skill link: %w", err)
	}
	logger.Info("disabled skill %q", name)
	return nil
}

// SyncEnabled 让已启用的 skill scope 对应的链接就位（会话加载时调用）。
//
// scope 启用状态按会话持久化，而链接按工作区存在：同一工作区的其它会话
// 停用某个 skill 后，本会话下次加载会重新补上链接。失败只记日志，不影响加载。
func SyncEnabled(workspace string, enabled map[string]bool) {
	if workspace == "" || len(enabled) == 0 {
		return
	}
	for _, s := range Discover(workspace) {
		if !enabled[s.Scope()] {
			continue
		}
		if err := Enable(workspace, s); err != nil {
			logger.Warn("sync skill %q failed: %v", s.Name, err)
		}
	}
}

// IsLinkedPath 判断 abs 是否为 .alkaid0/skills 下、且解析后落在某个 skill 目录里的路径。
//
// read 工具用它在 .alkaid0 保护之外放行技能内容；只放行真正的 skill 文件，
// 不打开任意软链接越界读取的口子。
func IsLinkedPath(workspace, abs string) bool {
	if workspace == "" {
		return false
	}
	// 先做纯词法包含判断：链接目录本身必然是真实的 .alkaid0/skills，
	// 不能用会解析符号链接的 skillpath.Within（它会把 demo/SKILL.md 解析到技能目录）。
	linkDir := filepath.Clean(skillpath.LinkDir(workspace))
	target := filepath.Clean(abs)
	rel, err := filepath.Rel(linkDir, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	for _, root := range skillpath.Roots(workspace, Alkaid0ConfigDir()) {
		if skillpath.Within(root.Path, real) {
			return true
		}
	}
	return false
}

// IsReadOnlyPath 判断 abs 是否落在只读的 skill 区域：
//
//   - <workspace>/.alkaid0/skills（已加载 skill 的链接目录）；
//   - 任一 skill 来源目录（工作区 .claude/skills、.agents/skills、用户目录等）。
//
// edit/file 工具据此拒绝改写技能定义，与沙盒内的只读策略保持一致。
// 路径允许尚不存在（新建文件）：此时退化为词法比较。
func IsReadOnlyPath(workspace, abs string) bool {
	if workspace == "" || abs == "" {
		return false
	}
	clean := filepath.Clean(abs)
	linkDir := filepath.Clean(skillpath.LinkDir(workspace))
	if rel, err := filepath.Rel(linkDir, clean); err == nil {
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	real := clean
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		real = resolved
	}
	for _, root := range skillpath.Roots(workspace, Alkaid0ConfigDir()) {
		if skillpath.Within(root.Path, clean) || skillpath.Within(root.Path, real) {
			return true
		}
	}
	return false
}

// load 读取 dir 下的 SKILL.md 并构造 Skill；没有入口文件或 frontmatter 非法时返回 false。
func load(dir, kind string) (Skill, bool) {
	manifest, ok := findManifest(dir)
	if !ok {
		return Skill{}, false
	}
	info, err := os.Stat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxManifestSize {
		return Skill{}, false
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		return Skill{}, false
	}
	raw, body, hasFrontmatter := splitFrontmatter(string(data))
	var fm frontmatter
	if hasFrontmatter {
		if err := yaml.Unmarshal([]byte(raw), &fm); err != nil {
			logger.Warn("skip skill %s: invalid frontmatter: %v", dir, err)
			return Skill{}, false
		}
	}
	name := sanitizeName(fm.Name)
	if name == "" {
		name = sanitizeName(filepath.Base(dir))
	}
	if name == "" {
		return Skill{}, false
	}
	desc := cleanDescription(fm.Description)
	if desc == "" {
		desc = firstLine(body)
	}
	if desc == "" {
		desc = "Skill " + name + " (read .alkaid0/skills/" + name + "/SKILL.md after enabling)"
	}
	return Skill{
		Name:        name,
		Description: desc,
		Dir:         dir,
		Source:      kind,
	}, true
}

// findManifest 在 dir 下查找入口文件（标准名 SKILL.md，兼容小写 skill.md）。
func findManifest(dir string) (string, bool) {
	for _, name := range []string{skillpath.ManifestName, "skill.md"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, true
		}
	}
	return "", false
}

// frontmatter SKILL.md 的 YAML 头部（只关心名称与描述）。
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// splitFrontmatter 拆分 "---\n<yaml>\n---\n<body>"，返回 yaml 段、正文与是否命中。
func splitFrontmatter(text string) (string, string, bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", text, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
	}
	return "", text, false
}

// sanitizeName 把 skill 名收敛为可安全用作 scope 后缀与链接名的形式。
func sanitizeName(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range s {
		// 保留字母（含中文等非 ASCII）、数字与 - _ .；其余（空格、路径分隔符等）
		// 统一换成 '-'，保证结果既能当 scope 名也能当链接名。
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	name := strings.Trim(b.String(), "-.")
	if runes := []rune(name); len(runes) > 64 {
		name = strings.Trim(string(runes[:64]), "-.")
	}
	return name
}

// cleanDescription 把描述压成单行并截断，保证 <scopes> 段不被多行内容撑破。
func cleanDescription(raw string) string {
	return truncateRunes(collapseSpaces(raw), maxDescriptionRunes)
}

// firstLine 取正文第一段可用文字作为描述（跳过空行与纯分隔线）。
func firstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if line == "" || strings.Trim(line, "-*_=") == "" {
			continue
		}
		return truncateRunes(collapseSpaces(line), maxDescriptionRunes)
	}
	return ""
}

// collapseSpaces 把换行/制表符与连续空白压成单个空格。
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateRunes 按字符截断并追加省略号。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// sameTarget 判断 link 是否已指向 dir（相对链接按 link 所在目录解析）。
func sameTarget(link, dir string) bool {
	target, err := os.Readlink(link)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}
	if filepath.Clean(target) == filepath.Clean(dir) {
		return true
	}
	realTarget, errTarget := filepath.EvalSymlinks(target)
	realDir, errDir := filepath.EvalSymlinks(dir)
	return errTarget == nil && errDir == nil && realTarget == realDir
}
