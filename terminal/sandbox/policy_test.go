package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// policyTestRoot 在用户家目录下创建测试根目录。
//
// 不能用 t.TempDir()：它位于系统临时目录，而策略把整个系统临时目录设为可写，
// 于是"工作区根目录只读"永远不会成立。家目录下除少数显式白名单
// （.gitconfig、gh 配置、shell 历史等）之外都没有写权限，与真实部署一致。
//
// 但容器里 HOME 常常就是系统临时目录（例如 HOME=/tmp）。此时前提不成立——
// 策略本来就把整个临时目录放进可写白名单，用例断言必然失败，而这种失败是
// 环境导致、与产品行为无关。这种情况直接跳过，避免把假失败当成回归。
func policyTestRoot(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skipf("无法定位用户家目录: %v", err)
	}
	if homeIsTempDir(home) {
		t.Skipf("家目录 %s 落在系统临时目录 %s 内，策略天然可写，跳过家目录口径的策略用例",
			home, os.TempDir())
	}
	root, err := os.MkdirTemp(home, ".alk-sandbox-policy-")
	if err != nil {
		t.Skipf("在家目录下创建测试目录失败（跳过策略用例）: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

// homeIsTempDir 报告家目录是否落在系统临时目录内（容器里常见的 HOME=/tmp）。
//
// 路径统一走 canonicalPath 归一化：macOS 上 /tmp 是 /private/tmp 的符号链接，
// 直接比字符串会漏判。
func homeIsTempDir(home string) bool {
	homeReal := canonicalPath(home)
	if homeReal == "" {
		return false
	}
	tmpReal := canonicalPath(os.TempDir())
	if tmpReal == "" {
		return false
	}
	return isWithin(homeReal, tmpReal)
}

// TestHomeIsTempDir 保证跳过逻辑只在 HOME 真的落在系统临时目录时生效：
// 一旦判定写错（比如恒为 true），整套策略用例会在所有环境静默跳过。
func TestHomeIsTempDir(t *testing.T) {
	tmp := canonicalPath(os.TempDir())
	if tmp == "" {
		t.Skip("无法定位系统临时目录")
	}
	if !homeIsTempDir(tmp) {
		t.Fatalf("homeIsTempDir(%q) = false, want true", tmp)
	}
	if !homeIsTempDir(filepath.Join(tmp, "alk-sandbox-fake-home")) {
		t.Fatalf("临时目录的子路径应判为临时目录")
	}
	notHome := filepath.Join(string(filepath.Separator)+"alkaid0-not-a-home", "user")
	if homeIsTempDir(notHome) {
		t.Fatalf("homeIsTempDir(%q) = true, want false", notHome)
	}
}

// newPolicyFixture 构造一个最小工作区/配置目录结构，返回策略与环境无关的路径。
func newPolicyFixture(t *testing.T) (ws, sub, cfg, logs string, venv string) {
	t.Helper()
	root := policyTestRoot(t)
	ws = filepath.Join(root, "ws")
	sub = filepath.Join(ws, "pkg", "sub")
	cfg = filepath.Join(root, "cfg")
	logs = filepath.Join(root, "logs")
	venv = filepath.Join(cfg, "venv")

	for _, dir := range []string{
		sub,
		filepath.Join(ws, ".alkaid0", "skills"),
		filepath.Join(ws, ".alkaid0", "temp"),
		filepath.Join(ws, ".alkaid0", "images"),
		cfg,
		logs,
		filepath.Join(venv, "bin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) failed: %v", dir, err)
		}
	}
	// .alkaid0 里放一个"机密"文件：策略必须让沙盒读不到它
	if err := os.WriteFile(filepath.Join(ws, ".alkaid0", "db.sqlite"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write db.sqlite failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write config.json failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logs, "log.log"), []byte("log"), 0o600); err != nil {
		t.Fatalf("write log.log failed: %v", err)
	}
	return ws, sub, cfg, logs, venv
}

// TestBuildPolicyRules 验证统一策略的规则推导（与平台无关）。
func TestBuildPolicyRules(t *testing.T) {
	ws, sub, cfg, logs, venv := newPolicyFixture(t)
	tmp := t.TempDir()

	p := buildPolicy(policyInput{
		workDir:      sub,
		workspace:    ws,
		tmpDir:       tmp,
		configDir:    cfg,
		logDir:       logs,
		writableDirs: []string{venv},
	})

	cases := []struct {
		path string
		want Access
	}{
		// 默认：全盘只读
		{"/etc/passwd", AccessReadOnly},
		{filepath.Join(ws, "README.md"), AccessReadOnly},
		// 可写：工作目录、临时目录、venv
		{sub, AccessReadWrite},
		{filepath.Join(sub, "main.go"), AccessReadWrite},
		{tmp, AccessReadWrite},
		{venv, AccessReadWrite},
		{filepath.Join(venv, "bin", "python"), AccessReadWrite},
		// 工作区 .alkaid0：仅可列出
		{filepath.Join(ws, ".alkaid0"), AccessListOnly},
		{filepath.Join(ws, ".alkaid0", "db.sqlite"), AccessListOnly},
		// 固定子目录：skills / images 只读（已加载的 skill 路径不可改写）
		{filepath.Join(ws, ".alkaid0", "skills"), AccessReadOnly},
		{filepath.Join(ws, ".alkaid0", "skills", "a", "SKILL.md"), AccessReadOnly},
		{filepath.Join(ws, ".alkaid0", "temp"), AccessReadWrite},
		{filepath.Join(ws, ".alkaid0", "images"), AccessReadOnly},
		{filepath.Join(ws, ".alkaid0", "images", "x.png"), AccessReadOnly},
		// 配置目录与日志目录：不可读写；venv 例外
		{cfg, AccessDeny},
		{filepath.Join(cfg, "config.json"), AccessDeny},
		{filepath.Join(cfg, "db.sqlite"), AccessDeny},
		{logs, AccessDeny},
		{filepath.Join(logs, "log.log"), AccessDeny},
	}

	for _, c := range cases {
		if got := p.AccessFor(c.path); got != c.want {
			t.Errorf("AccessFor(%q) = %s, want %s", c.path, got, c.want)
		}
	}

	// 策略里必须"提前建上" skills/temp/images
	for _, name := range []string{"skills", "temp", "images"} {
		dir := filepath.Join(ws, ".alkaid0", name)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("工作区 .alkaid0/%s 应该被自动创建: %v", name, err)
		}
	}

	// AllowedChildren 必须只包含 skills/temp/images（Windows 用它跳过封禁）
	allow := p.AllowedChildren()[canonicalPath(filepath.Join(ws, ".alkaid0"))]
	if len(allow) != 3 {
		t.Fatalf("AllowedChildren = %v, want 3 个子路径", allow)
	}
}

// TestBuildPolicyGitDirs 验证 .git 目录与 worktree 的 commondir 被登记为可写。
func TestBuildPolicyGitDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("worktree 布局在 Windows 上大小写/分隔符不同，另有覆盖")
	}
	root := policyTestRoot(t)
	ws := filepath.Join(root, "ws")
	sub := filepath.Join(ws, "sub")
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	p := buildPolicy(policyInput{workDir: sub, workspace: ws, tmpDir: root, configDir: filepath.Join(root, "cfg"), logDir: filepath.Join(root, "logs")})
	if got := p.AccessFor(filepath.Join(ws, ".git")); got != AccessReadWrite {
		t.Errorf(".git 应可读写，得到 %s", got)
	}
	if got := p.AccessFor(filepath.Join(ws, ".git", "index")); got != AccessReadWrite {
		t.Errorf(".git/index 应可读写，得到 %s", got)
	}

	// worktree/submodule：.git 是文件，指向真正的 git 目录
	wt := filepath.Join(root, "wt")
	wtGitDir := filepath.Join(root, "main", ".git", "worktrees", "wt")
	if err := os.MkdirAll(wtGitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../.."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p2 := buildPolicy(policyInput{workDir: wt, workspace: wt, tmpDir: root, configDir: filepath.Join(root, "cfg"), logDir: filepath.Join(root, "logs")})
	for _, path := range []string{wtGitDir, filepath.Join(root, "main", ".git")} {
		if got := p2.AccessFor(path); got != AccessReadWrite {
			t.Errorf("%s 应可读写（worktree/commondir），得到 %s", path, got)
		}
	}
}

// TestBuildPolicyProtectedAddsNoDirsOutsideWorkspace 确认策略不会把无关路径放开。
func TestBuildPolicyProtectedAddsNoDirsOutsideWorkspace(t *testing.T) {
	ws, sub, cfg, logs, _ := newPolicyFixture(t)
	p := buildPolicy(policyInput{
		workDir:   sub,
		workspace: ws,
		tmpDir:    filepath.Join(policyTestRoot(t), "tmp"),
		configDir: cfg,
		logDir:    logs,
	})
	for _, dir := range p.Paths(AccessReadWrite) {
		if dir == cfg || dir == logs {
			t.Errorf("配置/日志目录不应出现在可写列表中: %s", dir)
		}
		if isWithin(cfg, dir) && dir != filepath.Join(cfg, "venv") {
			t.Errorf("配置目录的子路径只应放开 venv，得到 %s", dir)
		}
	}
}

// TestBuildPolicySkillDirs 验证 skill 目录的读写口径：
//
//   - 工作区里的 .claude/skills、.agents/skills 属于普通工作区内容，跟随工作区可写；
//   - 只有 .alkaid0/skills（已启用技能的链接目录）收敛为只读；
//   - 配置目录下的 skills 位于 deny 的配置目录内，单独放行为只读。
func TestBuildPolicySkillDirs(t *testing.T) {
	root := policyTestRoot(t)
	ws := filepath.Join(root, "ws")
	for _, dir := range []string{
		filepath.Join(ws, ".claude", "skills", "a"),
		filepath.Join(ws, ".agents", "skills", "b"),
		filepath.Join(ws, ".alkaid0", "skills", "c"),
		filepath.Join(ws, ".alkaid0", "temp"),
		filepath.Join(ws, "src"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) failed: %v", dir, err)
		}
	}
	// 真实的 skill 入口文件，确保 canonicalPath 解析的是已存在路径。
	for _, file := range []string{
		filepath.Join(ws, ".claude", "skills", "a", "SKILL.md"),
		filepath.Join(ws, ".agents", "skills", "b", "SKILL.md"),
		filepath.Join(ws, ".alkaid0", "skills", "c", "SKILL.md"),
	} {
		if err := os.WriteFile(file, []byte("---\nname: x\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := buildPolicy(policyInput{
		workDir:   ws,
		workspace: ws,
		tmpDir:    filepath.Join(root, "tmp"),
		configDir: filepath.Join(root, "cfg"),
		logDir:    filepath.Join(root, "logs"),
	})
	cases := []struct {
		name string
		path string
		want Access
	}{
		{"工作区普通文件", filepath.Join(ws, "src", "main.go"), AccessReadWrite},
		{"工作区技能源目录（可写）", filepath.Join(ws, ".claude", "skills", "a", "SKILL.md"), AccessReadWrite},
		{"工作区技能源目录新文件（可写）", filepath.Join(ws, ".claude", "skills", "a", "new.md"), AccessReadWrite},
		{"工作区 .claude 其它文件", filepath.Join(ws, ".claude", "settings.json"), AccessReadWrite},
		{"工作区 .agents 技能源目录（可写）", filepath.Join(ws, ".agents", "skills", "b", "SKILL.md"), AccessReadWrite},
		{"链接目录（只读）", filepath.Join(ws, ".alkaid0", "skills", "c", "SKILL.md"), AccessReadOnly},
		{"链接目录下新文件（只读）", filepath.Join(ws, ".alkaid0", "skills", "c", "new.md"), AccessReadOnly},
		{".alkaid0/temp 可写", filepath.Join(ws, ".alkaid0", "temp", "t"), AccessReadWrite},
		{"配置目录下的 skills（只读）", filepath.Join(root, "cfg", "skills", "x", "SKILL.md"), AccessReadOnly},
		{"配置目录其它文件（deny）", filepath.Join(root, "cfg", "config.json"), AccessDeny},
	}
	for _, c := range cases {
		if got := p.AccessFor(c.path); got != c.want {
			t.Errorf("AccessFor(%q/%s) = %s, want %s", c.name, c.path, got, c.want)
		}
	}
}

// TestCanonicalPathSymlinkPrefix 校验"规则路径含符号链接、目标路径尚不存在"时的匹配。
//
// macOS 上 /tmp → /private/tmp、/var → /private/var。若只解析存在路径的符号链接，
// 规则会落到 /private/tmp，而还不存在的目标保持 /tmp/... 字面量，白名单永远匹配不上
// （CI 上 sandbox_test.go 的 TestDirectoryPermissions 就是这样在 macOS 挂掉的）。
func TestCanonicalPathSymlinkPrefix(t *testing.T) {
	root := policyTestRoot(t)
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}

	p := &Policy{}
	p.add(link, AccessReadWrite)
	p.add(filepath.Join(link, "sealed"), AccessReadOnly)

	cases := []struct {
		name string
		path string
		want Access
	}{
		{"符号链接下新建文件", filepath.Join(link, "new-file.txt"), AccessReadWrite},
		{"符号链接下多级新目录", filepath.Join(link, "a", "b", "new.txt"), AccessReadWrite},
		{"更深规则仍然生效", filepath.Join(link, "sealed", "new.txt"), AccessReadOnly},
		{"规则之外保持只读", filepath.Join(root, "outside", "new.txt"), AccessReadOnly},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.AccessFor(c.path); got != c.want {
				t.Fatalf("AccessFor(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}

	// 规则本身也应被规范化（存的是真实路径，而不是符号链接路径）
	if got := p.Paths(AccessReadWrite); len(got) != 1 || got[0] != real {
		t.Fatalf("Paths(rw) = %v, want [%s]", got, real)
	}
}
