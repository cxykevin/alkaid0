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
func policyTestRoot(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skipf("无法定位用户家目录: %v", err)
	}
	root, err := os.MkdirTemp(home, ".alk-sandbox-policy-")
	if err != nil {
		t.Skipf("在家目录下创建测试目录失败（跳过策略用例）: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
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
		// 固定子目录
		{filepath.Join(ws, ".alkaid0", "skills"), AccessReadWrite},
		{filepath.Join(ws, ".alkaid0", "skills", "a", "SKILL.md"), AccessReadWrite},
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
