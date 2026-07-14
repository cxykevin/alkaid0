package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// gitSandboxEnv 返回沙盒执行 git 所需的环境变量
func gitSandboxEnv() []string {
	env := os.Environ()
	env = append(env, "GIT_PAGER=cat")
	env = append(env, "PAGER=cat")
	return env
}

// Git 测试需要第三方软件（git），设置 ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE=true 启用

// TestGitBasicNone 测试无隔离模式下基本 git 操作
func TestGitBasicNone(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE") == "" {
		t.Skip("跳过第三方软件测试（设置 ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE=true 启用）")
	}
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows git 测试")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 未安装，跳过测试")
	}

	// 创建临时 git 仓库
	repoDir, err := os.MkdirTemp("", "sandbox-git-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(repoDir)

	// 在沙盒外初始化 git 仓库
	initCmd := exec.Command("git", "init", repoDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init 失败: %v\n%s", err, out)
	}
	// 设置 user config
	gitDir := "--git-dir=" + filepath.Join(repoDir, ".git")
	gitWorkTree := "--work-tree=" + repoDir
	for key, val := range map[string]string{
		"user.name":  "Sandbox Tester",
		"user.email": "tester@test.local",
	} {
		cmd := exec.Command("git", gitDir, gitWorkTree, "config", "--local", key, val)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config %s 失败: %v\n%s", key, err, out)
		}
	}
	// 创建初始提交
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("initial"), 0644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	addCmd := exec.Command("git", gitDir, gitWorkTree, "add", "README.md")
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add 失败: %v\n%s", err, out)
	}
	commitCmd := exec.Command("git", gitDir, gitWorkTree, "commit", "-m", "initial commit")
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit 失败: %v\n%s", err, out)
	}

	// 再添加几个提交
	for i := range 3 {
		file := filepath.Join(repoDir, fmt.Sprintf("file%d.txt", i))
		if err := os.WriteFile(file, fmt.Appendf(nil, "content %d", i), 0644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
		addCmd := exec.Command("git", gitDir, gitWorkTree, "add", fmt.Sprintf("file%d.txt", i))
		if out, err := addCmd.CombinedOutput(); err != nil {
			t.Fatalf("git add 失败: %v\n%s", err, out)
		}
		commitCmd := exec.Command("git", gitDir, gitWorkTree, "commit", "-m", fmt.Sprintf("commit %d", i))
		if out, err := commitCmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit 失败: %v\n%s", err, out)
		}
	}

	t.Run("git status", func(t *testing.T) {
		cfg := Config{
			WorkDir:       repoDir,
			Env:           gitSandboxEnv(),
			Timeout:       5 * time.Second,
			IsolationMode: IsolationNone,
		}
		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}
		cmd, err := sb.Execute("git", "status")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}
		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)
		if err := cmd.Run(); err != nil {
			t.Errorf("git status 执行失败: %v\nstderr: %s", err, stderr.String())
		}
		output := stdout.String()
		if !strings.Contains(output, "On branch") && !strings.Contains(output, "位于分支") && !strings.Contains(output, "main") {
			t.Errorf("git status 输出异常: %s", output)
		}
		t.Logf("git status 输出:\n%s", output)
	})

	t.Run("git log", func(t *testing.T) {
		cfg := Config{
			WorkDir:       repoDir,
			Env:           gitSandboxEnv(),
			Timeout:       5 * time.Second,
			IsolationMode: IsolationNone,
		}
		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}
		cmd, err := sb.Execute("git", "log", "--oneline")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}
		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)
		if err := cmd.Run(); err != nil {
			t.Errorf("git log 执行失败: %v\nstderr: %s", err, stderr.String())
		}
		output := strings.TrimSpace(stdout.String())
		lines := strings.Split(output, "\n")
		if len(lines) < 4 {
			t.Errorf("git log 应输出至少 4 行（4 个提交），实际 %d 行:\n%s", len(lines), output)
		}
		t.Logf("git log 输出 %d 行:\n%s", len(lines), output)
	})

	t.Run("git diff", func(t *testing.T) {
		// 修改一个文件后测试 diff
		if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("modified content"), 0644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
		cfg := Config{
			WorkDir:       repoDir,
			Env:           gitSandboxEnv(),
			Timeout:       5 * time.Second,
			IsolationMode: IsolationNone,
		}
		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}
		cmd, err := sb.Execute("git", "diff")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}
		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)
		if err := cmd.Run(); err != nil {
			t.Errorf("git diff 执行失败: %v\nstderr: %s", err, stderr.String())
		}
		output := stdout.String()
		if !strings.Contains(output, "modified content") {
			t.Errorf("git diff 应包含修改内容，实际输出:\n%s", output)
		}
		t.Logf("git diff 输出:\n%s", output)
	})
}

// TestGitInIsolation 测试 OS 隔离模式下 git 操作
func TestGitInIsolation(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE") == "" {
		t.Skip("跳过第三方软件测试（设置 ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE=true 启用）")
	}
	if runtime.GOOS != "linux" {
		t.Skip("OS 隔离模式仅支持 Linux")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 未安装，跳过测试")
	}

	// 创建临时 git 仓库
	repoDir, err := os.MkdirTemp("", "sandbox-git-test-isolated-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(repoDir)

	// 沙盒外 init + 首次提交
	initCmd := exec.Command("git", "init", repoDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init 失败: %v\n%s", err, out)
	}
	gitDir := "--git-dir=" + filepath.Join(repoDir, ".git")
	gitWorkTree := "--work-tree=" + repoDir
	for key, val := range map[string]string{
		"user.name":  "Sandbox Tester",
		"user.email": "tester@test.local",
	} {
		cmd := exec.Command("git", gitDir, gitWorkTree, "config", "--local", key, val)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config %s 失败: %v\n%s", key, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("initial"), 0644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	addCmd := exec.Command("git", gitDir, gitWorkTree, "add", "README.md")
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add 失败: %v\n%s", err, out)
	}
	commitCmd := exec.Command("git", gitDir, gitWorkTree, "commit", "-m", "initial commit")
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit 失败: %v\n%s", err, out)
	}

	t.Run("git status in OS isolation", func(t *testing.T) {
		cfg := Config{
			WorkDir:       repoDir,
			Env:           gitSandboxEnv(),
			Timeout:       10 * time.Second,
			IsolationMode: IsolationOS,
		}
		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}
		cmd, err := sb.Execute("git", "status")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}
		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)
		if err := cmd.Run(); err != nil {
			t.Errorf("git status 执行失败: %v\nstderr: %s", err, stderr.String())
		}
		output := stdout.String()
		if !strings.Contains(output, "On branch") && !strings.Contains(output, "位于分支") && !strings.Contains(output, "main") {
			t.Errorf("git status 输出异常: %s", output)
		}
		t.Logf("git status (OS isolation) 输出:\n%s", output)
	})

	t.Run("git log in OS isolation", func(t *testing.T) {
		cfg := Config{
			WorkDir:       repoDir,
			Env:           gitSandboxEnv(),
			Timeout:       10 * time.Second,
			IsolationMode: IsolationOS,
		}
		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}
		cmd, err := sb.Execute("git", "log", "--oneline")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}
		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)
		if err := cmd.Run(); err != nil {
			t.Errorf("git log 执行失败: %v\nstderr: %s", err, stderr.String())
		}
		output := strings.TrimSpace(stdout.String())
		if !strings.Contains(output, "initial") {
			t.Errorf("git log 输出异常:\n%s", output)
		}
		t.Logf("git log (OS isolation) 输出:\n%s", output)
	})

	t.Run("git add + commit in OS isolation", func(t *testing.T) {
		// 在沙盒内创建文件并提交
		script := `echo "new file" > test.txt && git add test.txt && git commit -m "sandbox commit"`
		cfg := Config{
			WorkDir:       repoDir,
			Env:           gitSandboxEnv(),
			Timeout:       10 * time.Second,
			IsolationMode: IsolationOS,
		}
		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}
		cmd, err := sb.Execute("sh", "-c", script)
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}
		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)
		if err := cmd.Run(); err != nil {
			t.Errorf("git add+commit 执行失败: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}
		t.Logf("git add+commit (OS isolation) 输出:\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())

		// 验证提交在沙盒外可见
		verify := exec.Command("git", gitDir, gitWorkTree, "log", "--oneline", "-1")
		out, _ := verify.CombinedOutput()
		if !strings.Contains(string(out), "sandbox commit") {
			t.Errorf("沙盒外验证 git log 未找到新提交:\n%s", string(out))
		}
	})
}

// TestGitLongLog 测试 git log 在无超时下正常完成（不 hang）
func TestGitLongLog(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE") == "" {
		t.Skip("跳过第三方软件测试（设置 ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE=true 启用）")
	}
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows git 测试")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 未安装，跳过测试")
	}

	repoDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取工作目录失败: %v", err)
	}
	// 尝试向上找到 .git 目录（在项目仓库上执行 git log）
	for {
		if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(repoDir)
		if parent == repoDir {
			t.Skip("未找到 git 仓库根目录")
		}
		repoDir = parent
	}
	t.Logf("使用 git 仓库: %s", repoDir)

	for _, mode := range []IsolationMode{IsolationNone, IsolationOS} {
		name := "none"
		if mode == IsolationOS {
			if runtime.GOOS != "linux" {
				continue
			}
			name = "os"
		}
		t.Run("isolation="+name, func(t *testing.T) {
			cfg := Config{
				WorkDir:       repoDir,
				Env:           gitSandboxEnv(),
				IsolationMode: mode,
				// 不设 Timeout（0=无超时），验证不会 hang
			}
			sb, err := New(cfg)
			if err != nil {
				t.Fatalf("创建沙盒失败: %v", err)
			}
			cmd, err := sb.Execute("git", "log", "--oneline", "-5")
			if err != nil {
				t.Fatalf("创建命令失败: %v", err)
			}
			var stdout, stderr bytes.Buffer
			cmd.SetStdout(&stdout)
			cmd.SetStderr(&stderr)

			done := make(chan error, 1)
			go func() {
				done <- cmd.Run()
			}()

			select {
			case err := <-done:
				if err != nil {
					t.Errorf("git log 执行失败: %v\nstderr: %s", err, stderr.String())
				}
				output := strings.TrimSpace(stdout.String())
				if !strings.Contains(output, "commit") && !strings.Contains(output, "feat") && !strings.Contains(output, "fix") {
					t.Errorf("git log 输出异常:\n%s", output)
				}
				t.Logf("git log (isolation=%s) 输出:\n%s", name, output)
			case <-time.After(15 * time.Second):
				t.Fatalf("git log (isolation=%s) 15 秒未返回，疑似 hang", name)
			}
		})
	}
}

// TestGitWithoutTimeout 在无显式超时下执行 git 命令（验证不会因默认超时被 kill）
func TestGitWithoutTimeout(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE") == "" {
		t.Skip("跳过第三方软件测试（设置 ALKAID0_TEST_SANDBOX_THIRDPARTY_SOFTWARE=true 启用）")
	}
	if runtime.GOOS == "windows" {
		t.Skip("跳过 Windows git 测试")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 未安装，跳过测试")
	}

	repoDir, err := os.MkdirTemp("", "sandbox-git-notimeout-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(repoDir)

	// 初始化 git 仓库
	initCmd := exec.Command("git", "init", repoDir)
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init 失败: %v\n%s", err, out)
	}
	gitDir := "--git-dir=" + filepath.Join(repoDir, ".git")
	gitWorkTree := "--work-tree=" + repoDir
	for key, val := range map[string]string{
		"user.name":  "Tester",
		"user.email": "test@test",
	} {
		cmd := exec.Command("git", gitDir, gitWorkTree, "config", "--local", key, val)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git config %s 失败: %v\n%s", key, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	addCmd := exec.Command("git", gitDir, gitWorkTree, "add", "README.md")
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add 失败: %v\n%s", err, out)
	}
	commitCmd := exec.Command("git", gitDir, gitWorkTree, "commit", "-m", "first")
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit 失败: %v\n%s", err, out)
	}

	// 测试各种 git 命令在不设超时时正常运行
	cmds := []struct {
		name string
		args []string
	}{
		{"git status", []string{"status"}},
		{"git log", []string{"log", "--oneline"}},
		{"git branch", []string{"branch"}},
		{"git rev-parse HEAD", []string{"rev-parse", "HEAD"}},
	}

	for _, tt := range cmds {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				WorkDir:       repoDir,
				Env:           gitSandboxEnv(),
				Timeout:       0, // 无超时
				IsolationMode: IsolationNone,
			}
			sb, err := New(cfg)
			if err != nil {
				t.Fatalf("创建沙盒失败: %v", err)
			}
			cmd, err := sb.Execute("git", tt.args...)
			if err != nil {
				t.Fatalf("创建命令失败: %v", err)
			}
			var stdout bytes.Buffer
			cmd.SetStdout(&stdout)

			done := make(chan error, 1)
			go func() {
				done <- cmd.Run()
			}()

			select {
			case err := <-done:
				if err != nil {
					t.Errorf("执行失败: %v", err)
				}
				t.Logf("%s 成功，输出: %s", tt.name, strings.TrimSpace(stdout.String()))
			case <-time.After(10 * time.Second):
				t.Fatalf("%s 10 秒未返回，疑似 hang", tt.name)
			}
		})
	}
}
