//go:build linux

package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这些用例验证"统一沙盒策略"在真实隔离环境里的落地效果：
// 全盘只读、工作区可写、.alkaid0 可列不可读写、配置/日志不可读写、venv 可读写可删、
// 以及沙盒内 git 的读与写。
//
// 前置条件：ALKAID0_TEST_SANDBOX=true 且当前环境支持非特权 user namespace。
// git 用例额外需要 ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE=true。

// sandboxSupported 检查当前环境能否创建 user+mount namespace。
func sandboxSupported(t *testing.T) {
	t.Helper()
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Skip("系统缺少 unshare")
	}
	if err := exec.Command(unshare, "--user", "--map-root-user", "true").Run(); err != nil {
		t.Skipf("当前环境不支持非特权 user namespace: %v", err)
	}
}

// policyFixture 一套用于策略验证的目录结构。
type policyFixture struct {
	root string
	ws   string
	sub  string
	cfg  string
	logs string
	venv string
	home string
	sb   *Sandbox
}

// newPolicyFixtureSandbox 构造目录结构并创建沙盒。
func newPolicyFixtureSandbox(t *testing.T) *policyFixture {
	t.Helper()
	root := policyTestRoot(t)
	f := &policyFixture{
		root: root,
		ws:   filepath.Join(root, "ws"),
		cfg:  filepath.Join(root, "cfg"),
		logs: filepath.Join(root, "logs"),
		home: filepath.Join(root, "home"),
	}
	f.sub = filepath.Join(f.ws, "pkg", "sub")
	f.venv = filepath.Join(f.cfg, "venv")

	for _, dir := range []string{
		f.sub,
		f.cfg,
		f.logs,
		f.home,
		filepath.Join(f.venv, "bin"),
		filepath.Join(f.ws, ".alkaid0", "images"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) failed: %v", dir, err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s failed: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s failed: %v", path, err)
		}
	}
	write(filepath.Join(f.ws, ".alkaid0", "db.sqlite"), "DB-SECRET")
	write(filepath.Join(f.ws, ".alkaid0", "images", "logo.png"), "PNG-DATA")
	write(filepath.Join(f.cfg, "config.json"), `{"key":"CONFIG-SECRET"}`)
	write(filepath.Join(f.logs, "log.log"), "LOG-SECRET")
	write(filepath.Join(f.venv, "bin", "python"), "#!/bin/sh\nexit 0\n")

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + f.home,
		"TMPDIR=" + os.TempDir(),
		"LANG=C",
		"LC_ALL=C",
		"GIT_PAGER=cat",
		"PAGER=cat",
		"GIT_CONFIG_NOSYSTEM=1",
	}

	sb, err := New(Config{
		WorkDir:       f.sub,
		Workspace:     f.ws,
		TmpDir:        os.TempDir(),
		ConfigDir:     f.cfg,
		LogDir:        f.logs,
		WritableDirs:  []string{f.venv},
		Env:           env,
		Timeout:       30 * time.Second,
		IsolationMode: IsolationOS,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	f.sb = sb
	return f
}

// run 在沙盒内执行脚本，返回合并输出与错误。
func (f *policyFixture) run(t *testing.T, script string) (string, error) {
	t.Helper()
	cmd, err := f.sb.Execute("sh", "-c", script)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	var out bytes.Buffer
	cmd.SetStdout(&out)
	cmd.SetStderr(&out)
	err = cmd.Run()
	return out.String(), err
}

// mustSucceed 断言脚本执行成功。
func (f *policyFixture) mustSucceed(t *testing.T, script string) string {
	t.Helper()
	out, err := f.run(t, script)
	if err != nil {
		t.Fatalf("沙盒内命令应成功: %v\n脚本: %s\n输出: %s", err, script, out)
	}
	return out
}

// mustFail 断言脚本执行失败（只读/不可读）。
func (f *policyFixture) mustFail(t *testing.T, script string) string {
	t.Helper()
	out, err := f.run(t, script)
	if err == nil {
		t.Fatalf("沙盒内命令应失败\n脚本: %s\n输出: %s", script, out)
	}
	return out
}

// TestSandboxPolicy 验证文件系统策略的每一项要求。
func TestSandboxPolicy(t *testing.T) {
	sandboxSupported(t)
	f := newPolicyFixtureSandbox(t)

	t.Run("全局只读", func(t *testing.T) {
		f.mustFail(t, "echo hacked > /etc/alk-sandbox-probe")
		f.mustSucceed(t, "cat /etc/hostname >/dev/null 2>&1 || true")
		// 工作区根目录（工作目录的上级）只读
		f.mustFail(t, fmt.Sprintf("echo hacked > %q", filepath.Join(f.ws, "probe.txt")))
	})

	t.Run("工作目录与临时目录可写", func(t *testing.T) {
		f.mustSucceed(t, fmt.Sprintf("echo ok > %q", filepath.Join(f.sub, "probe.txt")))
		f.mustSucceed(t, `echo ok > "$TMPDIR/alk-sandbox-tmp-probe"`)
		if data, err := os.ReadFile(filepath.Join(f.sub, "probe.txt")); err != nil || string(data) != "ok\n" {
			t.Fatalf("沙盒内写入未落到宿主工作目录: %v %q", err, data)
		}
	})

	t.Run("alkaid0可列出但不可读写", func(t *testing.T) {
		out := f.mustSucceed(t, fmt.Sprintf("ls -1 %q", filepath.Join(f.ws, ".alkaid0")))
		for _, name := range []string{"db.sqlite", "skills", "temp", "images"} {
			if !strings.Contains(out, name) {
				t.Errorf(".alkaid0 应能列出条目 %q，实际输出:\n%s", name, out)
			}
		}
		// 读内容必须失败，且不能泄露真实内容
		out = f.mustFail(t, fmt.Sprintf("cat %q", filepath.Join(f.ws, ".alkaid0", "db.sqlite")))
		if strings.Contains(out, "DB-SECRET") {
			t.Fatalf(".alkaid0 内容被泄露: %s", out)
		}
		// 写必须失败
		f.mustFail(t, fmt.Sprintf("echo hacked > %q", filepath.Join(f.ws, ".alkaid0", "new.txt")))
		f.mustFail(t, fmt.Sprintf("rm -f %q", filepath.Join(f.ws, ".alkaid0", "db.sqlite")))
		if _, err := os.Stat(filepath.Join(f.ws, ".alkaid0", "db.sqlite")); err != nil {
			t.Fatalf("沙盒内不应能删除 .alkaid0 里的文件: %v", err)
		}

		// skills / temp 可读写（含删除）
		f.mustSucceed(t, fmt.Sprintf("echo ok > %q && cat %q", filepath.Join(f.ws, ".alkaid0", "skills", "a.md"), filepath.Join(f.ws, ".alkaid0", "skills", "a.md")))
		f.mustSucceed(t, fmt.Sprintf("rm -f %q", filepath.Join(f.ws, ".alkaid0", "skills", "a.md")))
		f.mustSucceed(t, fmt.Sprintf("echo ok > %q && rm -f %q", filepath.Join(f.ws, ".alkaid0", "temp", "t"), filepath.Join(f.ws, ".alkaid0", "temp", "t")))

		// images 只读（可读不可写）
		f.mustSucceed(t, fmt.Sprintf("cat %q", filepath.Join(f.ws, ".alkaid0", "images", "logo.png")))
		f.mustFail(t, fmt.Sprintf("echo hacked > %q", filepath.Join(f.ws, ".alkaid0", "images", "new.png")))
	})

	t.Run("配置目录与日志目录不可读写", func(t *testing.T) {
		out := f.mustFail(t, fmt.Sprintf("cat %q", filepath.Join(f.cfg, "config.json")))
		if strings.Contains(out, "CONFIG-SECRET") {
			t.Fatalf("配置内容被泄露: %s", out)
		}
		f.mustFail(t, fmt.Sprintf("echo hacked > %q", filepath.Join(f.cfg, "evil.json")))
		out = f.mustFail(t, fmt.Sprintf("cat %q", filepath.Join(f.logs, "log.log")))
		if strings.Contains(out, "LOG-SECRET") {
			t.Fatalf("日志内容被泄露: %s", out)
		}
		f.mustFail(t, fmt.Sprintf("echo hacked > %q", filepath.Join(f.logs, "evil.log")))
	})

	t.Run("venv可读写可删", func(t *testing.T) {
		lib := filepath.Join(f.venv, "lib", "demo")
		f.mustSucceed(t, fmt.Sprintf("mkdir -p %q && echo data > %q && cat %q", lib, filepath.Join(lib, "f.txt"), filepath.Join(lib, "f.txt")))
		f.mustSucceed(t, fmt.Sprintf("rm -f %q && rmdir %q && rmdir %q", filepath.Join(lib, "f.txt"), lib, filepath.Join(f.venv, "lib")))
		if _, err := os.Stat(filepath.Join(f.venv, "lib")); !os.IsNotExist(err) {
			t.Fatalf("venv 内删除应作用于宿主（lib 目录应已消失）: %v", err)
		}
	})

	t.Run("git读写", func(t *testing.T) {
		if os.Getenv("ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE") == "" {
			t.Skip("跳过第三方软件测试（设置 ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE=true 启用）")
		}
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git 未安装")
		}
		git := func(args ...string) string {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = f.ws
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("git %v failed: %v\n%s", args, err, out)
			}
			return string(out)
		}
		git("init")
		git("config", "user.name", "Sandbox Tester")
		git("config", "user.email", "tester@test.local")
		if err := os.WriteFile(filepath.Join(f.ws, "README.md"), []byte("init\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "README.md")
		git("commit", "-m", "initial commit")
		// 仓库是在沙盒创建之后才初始化的，重建策略让 .git 进入可写白名单
		// （真实场景里仓库总是先存在；这里顺带覆盖 SetWorkDir 的策略重建）。
		if err := f.sb.SetWorkDir(f.sub); err != nil {
			t.Fatalf("SetWorkDir failed: %v", err)
		}
		if got := f.sb.GetPolicy().AccessFor(filepath.Join(f.ws, ".git")); got != AccessReadWrite {
			t.Fatalf(".git 应进入可写白名单，得到 %s", got)
		}

		// 沙盒内：读 .git（status/log），写 .git（add/commit）
		if err := os.WriteFile(filepath.Join(f.sub, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := f.mustSucceed(t, "git status --porcelain -uall")
		if !strings.Contains(out, "hello.txt") {
			t.Errorf("沙盒内 git status 未看到新文件:\n%s", out)
		}
		f.mustSucceed(t, "git add hello.txt && git commit -m 'sandbox commit'")

		// 沙盒外验证提交确实落库
		out = git("log", "--oneline", "-1")
		if !strings.Contains(out, "sandbox commit") {
			t.Fatalf("沙盒内提交未落到仓库:\n%s", out)
		}
		// .git 目录内的文件也应可写（git add 已经写过 index）
		if _, err := os.Stat(filepath.Join(f.ws, ".git", "index")); err != nil {
			t.Fatalf(".git/index 不存在: %v", err)
		}
	})
}
