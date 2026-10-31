package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/terminal/pythonenv"
)

// Access 路径在沙盒内的访问级别。
//
// 沙盒的默认状态是**全盘只读**（"整个 / 或 C:\ 可读不可写"），
// 只有命中规则的白名单目录才会被放开。规则按路径深度取最具体的一条：
// 深层规则覆盖浅层规则，因此 `<工作区>/.alkaid0` 的"仅可列出"不会影响
// 它下面的 `skills`、`temp`、`images`。
type Access int

const (
	// AccessReadWrite 可读可写（含新建与删除）。
	AccessReadWrite Access = iota
	// AccessReadOnly 只读。未命中任何规则时的默认级别。
	AccessReadOnly
	// AccessListOnly 不可读内容、不可写，但可以列出目录条目。
	AccessListOnly
	// AccessDeny 不可读写，目录以空视图隐藏（配置目录与日志目录）。
	AccessDeny
)

// String 返回访问级别的可读名称（日志与测试使用）。
func (a Access) String() string {
	switch a {
	case AccessReadWrite:
		return "rw"
	case AccessReadOnly:
		return "ro"
	case AccessListOnly:
		return "list"
	case AccessDeny:
		return "deny"
	default:
		return "unknown"
	}
}

// 工作区内由 alkaid0 自己维护的目录名。
const (
	alkaid0DirName = ".alkaid0"
	alkaid0Skills  = "skills"
	alkaid0Temp    = "temp"
	alkaid0Images  = "images"
	// alkaid0VenvName 与 terminal/pythonenv 的 venv 目录名一致。
	alkaid0VenvName = "venv"
)

// PathRule 一条路径访问规则。
type PathRule struct {
	Path   string
	Access Access
}

// Policy 沙盒文件系统访问策略。
//
// Windows 沙盒（受限令牌 + DACL）与 Linux 沙盒（chroot + 挂载）使用
// **同一份策略**，只是执行手段不同：Windows 用 "拒绝/授予 ACL"，
// Linux 用 "只读根 + 定向 remount/临时文件系统覆盖"。
type Policy struct {
	rules []PathRule
}

// add 记录一条规则；同一路径重复出现时保留更严格的那条（安全优先）。
func (p *Policy) add(path string, access Access) {
	path = canonicalPath(path)
	if path == "" || path == string(filepath.Separator) {
		return
	}
	for i := range p.rules {
		if p.rules[i].Path == path {
			if access > p.rules[i].Access {
				p.rules[i].Access = access
			}
			return
		}
	}
	p.rules = append(p.rules, PathRule{Path: path, Access: access})
}

// Rules 返回按路径深度升序（浅 → 深）排列的规则副本。
// 执行方按此顺序应用规则，深层规则自然覆盖浅层规则。
func (p *Policy) Rules() []PathRule {
	out := make([]PathRule, len(p.rules))
	copy(out, p.rules)
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := pathDepth(out[i].Path), pathDepth(out[j].Path)
		if di != dj {
			return di < dj
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Paths 返回指定访问级别的全部路径。
func (p *Policy) Paths(access Access) []string {
	var out []string
	for _, r := range p.rules {
		if r.Access == access {
			out = append(out, r.Path)
		}
	}
	sort.Strings(out)
	return out
}

// AllowedChildren 返回"仅可列出"目录下仍然放开的子路径（skills/temp/images 等）。
// Linux 用 tmpfs + 定向 bind 天然按路径覆盖；Windows 不能像 Linux 那样用 tmpfs + 定向 bind 表达"可列不可读"，
// 需要知道白名单子项才能逐个跳过封禁。
func (p *Policy) AllowedChildren() map[string][]string {
	out := make(map[string][]string)
	for _, seal := range p.rules {
		if seal.Access != AccessDeny && seal.Access != AccessListOnly {
			continue
		}
		for _, r := range p.rules {
			if r.Path == seal.Path || !isWithin(r.Path, seal.Path) {
				continue
			}
			if r.Access == AccessReadWrite || r.Access == AccessReadOnly {
				out[seal.Path] = append(out[seal.Path], r.Path)
			}
		}
	}
	return out
}

// AccessFor 返回路径命中的最深规则；未命中任何规则时返回全局默认（只读）。
func (p *Policy) AccessFor(path string) Access {
	target := canonicalPath(path)
	if target == "" {
		return AccessReadOnly
	}
	best := AccessReadOnly
	bestDepth := -1
	for _, r := range p.rules {
		if !isWithin(target, r.Path) {
			continue
		}
		d := pathDepth(r.Path)
		if d > bestDepth || (d == bestDepth && r.Access > best) {
			best, bestDepth = r.Access, d
		}
	}
	return best
}

// Writable 报告路径在沙盒内是否可写。
func (p *Policy) Writable(path string) bool {
	return p.AccessFor(path) == AccessReadWrite
}

// policyInput 策略输入。ConfigDir / LogDir 留空时按进程环境自动检测；
// 显式指定主要用于测试与嵌入式调用（把 alkaid0 配置目录换到临时目录）。
type policyInput struct {
	workDir      string
	workspace    string
	tmpDir       string
	configDir    string
	logDir       string
	writableDirs []string
}

// buildPolicy 根据沙盒配置与进程环境推导访问策略。
func buildPolicy(in policyInput) *Policy {
	p := &Policy{}

	// 1) 可读写：沙盒临时目录、工作目录、调用方显式白名单、系统临时目录。
	for _, dir := range append([]string{in.tmpDir, in.workDir}, in.writableDirs...) {
		p.add(dir, AccessReadWrite)
	}
	for _, dir := range systemTempDirs() {
		p.add(dir, AccessReadWrite)
	}

	// 2) alkaid0 自身：配置目录与日志目录不可读写；
	//    venv 例外——它在配置目录里，必须整体可读写（含删除）。
	configDir := in.configDir
	if configDir == "" {
		configDir = alkaid0ConfigDir()
	}
	logDir := in.logDir
	if logDir == "" {
		logDir = alkaid0LogDir()
	}
	p.add(configDir, AccessDeny)
	p.add(logDir, AccessDeny)
	if in.configDir != "" {
		// 显式指定配置目录时 venv 跟随它（测试/嵌入式场景）
		p.add(filepath.Join(in.configDir, alkaid0VenvName), AccessReadWrite)
	} else if venv := pythonenv.VenvDirForConfig(config.Path()); venv != "" {
		p.add(venv, AccessReadWrite)
	}

	// 3) 所有工作区的 .alkaid0：仅可列出条目；skills/temp 可读写，images 只读。
	for _, dir := range workspaceScanDirs(in.workDir, in.workspace, in.writableDirs) {
		alk := filepath.Join(dir, alkaid0DirName)
		if !isRealDir(alk) {
			continue
		}
		p.addWorkspaceAlkaid0(alk)
	}

	// 4) git：仓库 .git 目录要能写；全局配置只需要读（默认即只读）。
	for _, dir := range ancestorDirs(in.workDir, in.workspace) {
		p.addGitRepo(dir)
	}
	p.addGitConfigs()

	// 5) shell 与常用命令行工具的运行时目录。
	p.addShellState()
	p.addToolState()

	return p
}

// addWorkspaceAlkaid0 把工作区 .alkaid0 及其固定子目录登记成策略。
// 目录不存在时按需求"提前建上"（skills/temp/images）。
func (p *Policy) addWorkspaceAlkaid0(alk string) {
	p.add(alk, AccessListOnly)

	skills := filepath.Join(alk, alkaid0Skills)
	temp := filepath.Join(alk, alkaid0Temp)
	images := filepath.Join(alk, alkaid0Images)
	for _, dir := range []string{skills, temp, images} {
		if err := MkdirAllConfigured(dir, 0o755); err != nil {
			logger.Warn("create workspace dir %s failed: %v", dir, err)
		}
	}
	p.add(skills, AccessReadWrite)
	p.add(temp, AccessReadWrite)
	p.add(images, AccessReadOnly)
}

// addGitRepo 登记 dir 下 git 仓库的可写目录。
//   - 普通仓库：<dir>/.git 目录可读写；
//   - worktree / submodule：.git 是文件，内容是 "gitdir: <路径>"，
//     被指向的目录（以及 commondir 指向的主仓库）可读写；
//   - 裸仓库：HEAD + objects + refs 齐备时整个目录可读写。
func (p *Policy) addGitRepo(dir string) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Lstat(dotGit)
	if err == nil {
		if info.IsDir() {
			p.add(dotGit, AccessReadWrite)
		} else if target := readGitDirFile(dotGit); target != "" {
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			p.add(target, AccessReadWrite)
			// worktree 的 refs/objects 在主仓库里，由 commondir 指出。
			if common := readCommonDirFile(target); common != "" {
				if !filepath.IsAbs(common) {
					common = filepath.Join(target, common)
				}
				p.add(common, AccessReadWrite)
			}
		}
	}

	// 裸仓库（或 git dir 本身就是工作目录）
	if fileExists(filepath.Join(dir, "HEAD")) &&
		isRealDir(filepath.Join(dir, "objects")) &&
		isRealDir(filepath.Join(dir, "refs")) {
		p.add(dir, AccessReadWrite)
	}
}

// addGitConfigs 登记 git 全局配置的读取白名单。
//
// 全盘默认只读，因此 Linux 上这些路径本来就"可读"；把它们显式登记成规则
// 是为了让策略本身可断言，并且保证 Windows（默认读由系统 ACL 决定）与
// Linux 行为一致。
func (p *Policy) addGitConfigs() {
	home, _ := os.UserHomeDir()
	var files []string
	if home != "" {
		files = append(files,
			filepath.Join(home, ".gitconfig"),
			filepath.Join(home, ".git-credentials"),
			filepath.Join(home, ".netrc"),
			filepath.Join(home, ".ssh", "config"),
			filepath.Join(home, ".ssh", "known_hosts"),
		)
	}
	if xdg := xdgConfigHome(home); xdg != "" {
		files = append(files,
			filepath.Join(xdg, "git", "config"),
			filepath.Join(xdg, "git", "ignore"),
			filepath.Join(xdg, "git", "attributes"),
		)
	}
	if runtime.GOOS == "windows" {
		// Windows 上 git 的"全局"配置分布在多个位置，等价于 Linux 的 /etc/gitconfig。
		for _, base := range []string{
			os.Getenv("ProgramData"),
			os.Getenv("ALLUSERSPROFILE"),
		} {
			if base != "" {
				files = append(files, filepath.Join(base, "Git", "config"))
			}
		}
		for _, pf := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432")} {
			if pf == "" {
				continue
			}
			files = append(files,
				filepath.Join(pf, "Git", "etc", "gitconfig"),
				filepath.Join(pf, "Git", "mingw64", "etc", "gitconfig"),
			)
		}
	}
	for _, f := range files {
		p.add(f, AccessReadOnly)
	}
}

// addShellState 给 bash/zsh/powershell 等 shell 的运行时文件登记写权限。
//
// shell 在启动/退出时会写历史文件与缓存；这些路径默认只读会让 shell 报
// "只读文件系统"错误（bash 静默、powershell 每次启动都慢且告警）。
func (p *Policy) addShellState() {
	home, _ := os.UserHomeDir()
	if home != "" {
		for _, name := range []string{
			".bash_history",
			".zsh_history",
			".ash_history",
			".sh_history",
			filepath.Join(".local", "share", "zsh", "history"),
		} {
			p.add(filepath.Join(home, name), AccessReadWrite)
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	// PowerShell：PSReadLine 历史与模块分析缓存都落在用户 AppData 下。
	for _, base := range []string{os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA")} {
		if base == "" {
			continue
		}
		p.add(filepath.Join(base, "Microsoft", "Windows", "PowerShell"), AccessReadWrite)
	}
}

// addToolState 给 gh 等命令行工具的配置/缓存目录登记写权限。
// （gh 在 token 刷新、读取 hosts.yml 时会写配置目录。）
func (p *Policy) addToolState() {
	home, _ := os.UserHomeDir()
	if configDir := os.Getenv("GH_CONFIG_DIR"); configDir != "" {
		p.add(configDir, AccessReadWrite)
	} else if xdg := xdgConfigHome(home); xdg != "" {
		p.add(filepath.Join(xdg, "gh"), AccessReadWrite)
	}
	if xdg := xdgCacheHome(home); xdg != "" {
		p.add(filepath.Join(xdg, "gh"), AccessReadWrite)
	}
	if xdg := xdgStateHome(home); xdg != "" {
		p.add(filepath.Join(xdg, "gh"), AccessReadWrite)
	}
	if runtime.GOOS != "windows" {
		return
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		p.add(filepath.Join(appdata, "GitHub CLI"), AccessReadWrite)
		p.add(filepath.Join(appdata, "gh"), AccessReadWrite)
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		p.add(filepath.Join(local, "GitHub CLI"), AccessReadWrite)
	}
}

// alkaid0ConfigDir 返回 alkaid0 配置/数据目录（配置文件所在目录）。
func alkaid0ConfigDir() string {
	path := config.Path()
	if path == "" {
		return ""
	}
	return filepath.Dir(configutil.ExpandPath(path))
}

// alkaid0LogDir 返回日志目录。
func alkaid0LogDir() string {
	return log.Dir()
}

// systemTempDirs 返回各平台的系统临时目录（始终可读写）。
func systemTempDirs() []string {
	dirs := []string{os.TempDir()}
	if runtime.GOOS == "windows" {
		dirs = append(dirs, os.Getenv("TEMP"), os.Getenv("TMP"), `C:\Windows\Temp`)
	} else {
		dirs = append(dirs, "/tmp", "/var/tmp")
	}
	return dirs
}

// workspaceScanDirs 返回需要检查 .alkaid0 的目录：
// 工作目录到工作区根之间的各级目录，以及所有显式白名单目录。
func workspaceScanDirs(workDir, workspace string, writableDirs []string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(dir string) {
		dir = canonicalPath(dir)
		if dir == "" {
			return
		}
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	for _, dir := range ancestorDirs(workDir, workspace) {
		add(dir)
	}
	add(workspace)
	for _, dir := range writableDirs {
		add(dir)
	}
	return out
}

// ancestorDirs 返回 dir 及其向上直到 stop（含 stop）的各级目录。
// stop 为空、不是目录或不是 dir 的祖先时，只返回 dir 本身。
func ancestorDirs(dir, stop string) []string {
	dir = canonicalPath(dir)
	if dir == "" {
		return nil
	}
	stop = canonicalPath(stop)
	out := []string{dir}
	if stop == "" || stop == dir || !isWithin(dir, stop) {
		return out
	}
	for cur := dir; ; {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		out = append(out, parent)
		if parent == stop {
			break
		}
		cur = parent
	}
	return out
}

// canonicalPath 把路径规范成绝对、无符号链接的形式，用于规则路径与待检查路径的统一比较。
//
// 待检查的路径通常**还不存在**（要新建的文件、要创建的目录）。若只对存在的路径做
// EvalSymlinks，规则路径会被解析（macOS 上 /tmp → /private/tmp、/var → /private/var），
// 而不存在的目标保持字面量，两边落在不同字符串上，白名单永远匹配不上
// （CI 上表现为 macOS 的 /tmp、/var/log 判定为不可写）。
// 因此这里对不存在的路径回退到"最近的存在祖先"解析后再拼回剩余部分。
func canonicalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	p := filepath.Clean(configutil.ExpandPath(path))
	if !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	if real, ok := evalSymlinksPartial(p); ok {
		p = real
	}
	return p
}

// evalSymlinksPartial 解析路径中的符号链接；路径尾部不存在时，
// 解析最近的存在祖先并把剩余部分原样拼回。
func evalSymlinksPartial(p string) (string, bool) {
	rest := ""
	cur := p
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil && real != "" {
			if rest == "" {
				return filepath.Clean(real), true
			}
			return filepath.Clean(filepath.Join(real, rest)), true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// pathDepth 返回路径层级（分隔符数量），用于"最深规则优先"的排序。
func pathDepth(path string) int {
	return strings.Count(filepath.Clean(path), string(filepath.Separator))
}

// isWithin 报告 path 是否等于 dir 或位于 dir 之下。
func isWithin(path, dir string) bool {
	if path == dir {
		return true
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isRealDir 报告 path 是否存在且是目录（跟随符号链接）。
func isRealDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// fileExists 报告 path 是否存在（任意类型）。
func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// readGitDirFile 读取 .git 文件里的 "gitdir: <路径>"。
func readGitDirFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "gitdir:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// readCommonDirFile 读取 git worktree 的 commondir 文件。
func readCommonDirFile(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func xdgConfigHome(home string) string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("APPDATA"); v != "" {
			return v
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config")
}

func xdgCacheHome(home string) string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return v
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cache")
}

func xdgStateHome(home string) string {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return v
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state")
}
