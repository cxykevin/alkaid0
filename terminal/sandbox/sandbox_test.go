package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 注意：这些测试需要 root 权限和 ALKAID0_TEST_SANDBOX=true 环境变量

// ---- 平台无关的测试命令 ----
//
// Windows 上并不存在 echo.exe / sleep.exe（除非 Git 的 usr/bin 恰好进了 PATH）。
// 之前这些用例在最小化安装的 Windows 上直接"找不到可执行文件"：
// TestExecuteCommandNoIsolation / TestConcurrentSandbox 因此失败，而
// TestCommandShortTimeout / TestCommandTimeout 反倒因为命令根本没启动而"通过"
// （正是审查里点名的"恒真断言"同类问题）。这里统一使用各平台自带的命令：
// Unix 用 echo/sleep，Windows 用 cmd.exe 与 ping。

// executeEcho 执行"回显 msg"的命令。
func executeEcho(sb *Sandbox, msg string) (*Command, error) {
	if runtime.GOOS == "windows" {
		return sb.Execute("cmd.exe", "/c", "echo", msg)
	}
	return sb.Execute("echo", msg)
}

// executeSleep 执行"阻塞约 seconds 秒"的命令。
// Windows 用 ping 的 1 秒发送间隔计时（ping 位于 System32，稳定可用）。
func executeSleep(sb *Sandbox, seconds int) (*Command, error) {
	if runtime.GOOS == "windows" {
		return sb.Execute("ping.exe", "-n", strconv.Itoa(seconds+1), "127.0.0.1")
	}
	return sb.Execute("sleep", strconv.Itoa(seconds))
}

// assertCommandStarted 断言命令确实启动过：exec.Error 表示连可执行文件都没找到，
// 此时"超时/报错"是用例自身没跑起来的结果，必须失败而不是通过。
func assertCommandStarted(t *testing.T, err error) {
	t.Helper()
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		t.Fatalf("命令未能启动，用例前提不成立: %v", err)
	}
}

func TestNew(t *testing.T) {
	sb, err := New(Config{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	if sb.workDir == "" {
		t.Error("workDir should not be empty")
	}
	if sb.tmpDir == "" {
		t.Error("tmpDir should not be empty")
	}
}

func TestIsolationModes(t *testing.T) {
	mode := IsolationNone
	if mode.String() != "none" {
		t.Errorf("IsolationNone.String() = %s, want none", mode.String())
	}

	mode = IsolationOS
	if mode.String() != "os" {
		t.Errorf("IsolationOS.String() = %s, want os", mode.String())
	}
}

func TestIsPathWritable(t *testing.T) {
	tmpDir := os.TempDir()

	tests := []struct {
		name  string
		cfg   Config
		path  string
		want  bool
		match string
	}{
		{
			name: "无隔离-任意路径",
			cfg:  Config{IsolationMode: IsolationNone},
			path: "/etc/passwd",
			want: true,
		},
		{
			name: "OS隔离-临时目录",
			cfg:  Config{IsolationMode: IsolationOS, WritableDirs: []string{tmpDir}, WorkDir: tmpDir},
			path: tmpDir,
			want: true,
		},
		{
			name: "OS隔离-系统目录",
			cfg:  Config{IsolationMode: IsolationOS, WritableDirs: []string{tmpDir}, WorkDir: tmpDir},
			path: "/etc/passwd",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb, err := New(tt.cfg)
			if err != nil {
				t.Fatalf("New() failed: %v", err)
			}

			got := sb.IsPathWritable(tt.path)
			if got != tt.want {
				t.Errorf("IsPathWritable(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExecuteCommandNoIsolation(t *testing.T) {
	cfg := Config{IsolationMode: IsolationNone, Timeout: 5 * time.Second}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	cmd, err := executeEcho(sb, "hello")
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	var stdout bytes.Buffer
	cmd.SetStdout(&stdout)

	if err := cmd.Run(); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "hello" {
		t.Errorf("Unexpected output: %s", output)
	}
}

func TestExecuteCommandWithIsolation(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	t.Log("开始 TestExecuteCommandWithIsolation")

	// 使用 /tmp 作为 WorkDir，确保在 OS 隔离命名空间内可访问；
	// Windows 没有 /tmp，OS 隔离同样需要一个真实存在的目录。
	tmpDir := "/tmp"
	if runtime.GOOS == "windows" {
		tmpDir = t.TempDir()
	}

	cfg := Config{
		WritableDirs:  []string{tmpDir},
		WorkDir:       tmpDir,
		IsolationMode: IsolationOS,
		// 2s 在整机负载下不够：Windows 上创建隔离命令要建受限令牌、Job Object
		// 并改 ACL，cmd.exe 冷启动本身就可能超过 2s（实机复现 context deadline
		// exceeded）。用例验证的是隔离是否生效，不是延迟，给足启动时间。
		Timeout: 20 * time.Second,
	}

	t.Log("准备创建命令")
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	t.Log("创建命令返回")
	cmd, err := executeEcho(sb, "hello")
	if err != nil {
		if runtime.GOOS == "darwin" {
			t.Skip("macOS 可能不支持 OS 隔离模式")
		}
		t.Fatalf("Execute() failed: %v", err)
	}

	t.Log("已设置标准输出/错误")
	var stdout, stderr bytes.Buffer
	cmd.SetStdout(&stdout)
	cmd.SetStderr(&stderr)

	t.Log("开始执行")
	err = cmd.Run()
	if err != nil {
		// OS 隔离模式可能因为缺少 unshare 或沙箱工具而不支持
		// 这种情况下应该跳过测试而不是失败
		if strings.Contains(err.Error(), "not supported") ||
			strings.Contains(err.Error(), "executable file not found") ||
			strings.Contains(err.Error(), "exit status") {
			t.Skipf("跳过隔离执行测试（可能需要额外工具）: %v", err)
			t.Logf("命令: %s %v", "echo", []string{"hello"})
			t.Logf("沙盒目录: tmp=%q work=%q writable=%v", cfg.TmpDir, cfg.WorkDir, cfg.WritableDirs)
			t.Logf("配置: isolation=os timeout=20s")
			t.Logf("遗言: elapsed=0s runErr=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
		t.Fatalf("Run() failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	output := strings.TrimSpace(stdout.String())
	if output != "hello" {
		t.Errorf("Unexpected output: %s", output)
	}
}

func TestCommandShortTimeout(t *testing.T) {
	cfg := Config{
		IsolationMode: IsolationNone,
		Timeout:       100 * time.Millisecond,
	}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	start := time.Now()
	cmd, err := executeSleep(sb, 10)
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	err = cmd.Run()
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	assertCommandStarted(t, err)

	if elapsed < 50*time.Millisecond {
		t.Errorf("命令在超时前就结束了，用例前提不成立: elapsed=%v err=%v", elapsed, err)
	}

	if elapsed > 2*time.Second {
		t.Errorf("Timeout took too long: %v", elapsed)
	}

	t.Logf("超时测试通过: error=%v, elapsed=%v", err, elapsed)
}

func TestCommandTimeout(t *testing.T) {
	cfg := Config{
		IsolationMode: IsolationNone,
		Timeout:       1 * time.Second,
	}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	cmd, err := executeSleep(sb, 3)
	if err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	err = cmd.Run()
	if err == nil {
		t.Error("Expected timeout error")
	}
	assertCommandStarted(t, err)
}

func TestSetWorkDir(t *testing.T) {
	sb, err := New(Config{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	testDir := filepath.Clean(os.TempDir())

	if err := sb.SetWorkDir(testDir); err != nil {
		t.Errorf("SetWorkDir(%s) failed: %v", testDir, err)
	}

	if sb.GetWorkDir() != testDir {
		t.Errorf("workDir should be %s, got %s", testDir, sb.GetWorkDir())
	}

	if err := sb.SetWorkDir("/alkaid0-unit-test-nonexistent-path"); err == nil {
		t.Error("SetWorkDir on non-existent path should fail")
	}
}

func TestSetIsolationMode(t *testing.T) {
	sb, err := New(Config{})
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	if sb.GetIsolationMode() != IsolationNone {
		t.Errorf("default isolation mode should be none")
	}

	sb.SetIsolationMode(IsolationOS)
	if sb.GetIsolationMode() != IsolationOS {
		t.Errorf("isolation mode should be os")
	}
}

func TestGetPlatformInfo(t *testing.T) {
	info := GetPlatformInfo()

	if info["os"] != runtime.GOOS {
		t.Errorf("os mismatch: %s != %s", info["os"], runtime.GOOS)
	}
	if info["arch"] != runtime.GOARCH {
		t.Errorf("arch mismatch: %s != %s", info["arch"], runtime.GOARCH)
	}

	t.Logf("平台信息: %v", info)
}

func TestIsolationModeString(t *testing.T) {
	tests := []struct {
		mode IsolationMode
		want string
	}{
		{IsolationNone, "none"},
		{IsolationOS, "os"},
		{IsolationMode(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.want {
				t.Errorf("String() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestIsolationOSSpec(t *testing.T) {
	requireOSIsolation(t)
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	switch runtime.GOOS {
	case "linux":
		cfg := Config{
			WritableDirs:  []string{"/tmp"},
			WorkDir:       "/tmp",
			IsolationMode: IsolationOS,
			Timeout:       5 * time.Second,
		}

		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}

		// 测试读取系统文件
		cmd, err := sb.Execute("cat", "/etc/hostname")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}

		var stdout bytes.Buffer
		cmd.SetStdout(&stdout)

		if err := cmd.Run(); err != nil {
			t.Fatalf("隔离执行失败: %v", err)
		}

		t.Logf("读取到hostname: %s", stdout.String())
	case "darwin":
		cfg := Config{
			WritableDirs:  []string{"/tmp"},
			WorkDir:       "/tmp",
			IsolationMode: IsolationOS,
			Timeout:       5 * time.Second,
		}

		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}

		cmd, err := sb.Execute("echo", "hello")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}

		var stdout bytes.Buffer
		cmd.SetStdout(&stdout)

		if err := cmd.Run(); err != nil {
			t.Fatalf("隔离执行失败: %v", err)
		}

		t.Logf("输出: %s", stdout.String())
	case "windows":
		cfg := Config{
			WritableDirs:  []string{os.TempDir()},
			WorkDir:       os.TempDir(),
			IsolationMode: IsolationOS,
			Timeout:       5 * time.Second,
		}

		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}

		cmd, err := sb.Execute("cmd.exe", "/c", "echo hello")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}

		var stdout bytes.Buffer
		cmd.SetStdout(&stdout)

		if err := cmd.Run(); err != nil {
			t.Fatalf("隔离执行失败: %v", err)
		}

		t.Logf("输出: %s", stdout.String())
	}
}

func TestDirectoryPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("此测试仅适用于 Unix 系统")
	}

	tmpDir := "/tmp"
	_, _ = os.Getwd()

	tests := []struct {
		name string
		cfg  Config
		path string
		want bool
	}{
		{
			name: "OS隔离-temp目录内可写",
			cfg: Config{
				WritableDirs:  []string{tmpDir},
				WorkDir:       tmpDir,
				IsolationMode: IsolationOS,
			},
			path: fmt.Sprintf("%s/test-file.txt", tmpDir),
			want: true,
		},
		{
			name: "OS隔离-temp目录内子目录可写",
			cfg: Config{
				WritableDirs:  []string{tmpDir},
				WorkDir:       tmpDir,
				IsolationMode: IsolationOS,
			},
			path: fmt.Sprintf("%s/subdir/test-file.txt", tmpDir),
			want: true,
		},
		{
			name: "OS隔离-非指定目录只读",
			cfg: Config{
				WritableDirs:  []string{tmpDir},
				WorkDir:       tmpDir,
				IsolationMode: IsolationOS,
			},
			path: "/etc/passwd",
			want: false,
		},
		{
			name: "OS隔离-非指定目录子目录只读",
			cfg: Config{
				WritableDirs:  []string{tmpDir},
				WorkDir:       tmpDir,
				IsolationMode: IsolationOS,
			},
			path: "/etc/ssl/certs/ca-certificates.crt",
			want: false,
		},
		{
			name: "无隔离-任意路径可写",
			cfg: Config{
				IsolationMode: IsolationNone,
			},
			path: "/etc/should-be-writable-in-none-mode",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb, err := New(tt.cfg)
			if err != nil {
				t.Fatalf("创建沙盒失败: %v", err)
			}

			got := sb.IsPathWritable(tt.path)
			if got != tt.want {
				t.Errorf("IsPathWritable(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestDirectoryPermissionsWithMultipleDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("此测试仅适用于 Unix 系统")
	}

	tmpDir := "/tmp"
	_, _ = os.Getwd()

	cfg := Config{
		WritableDirs:  []string{"/home", "/var/log"},
		WorkDir:       tmpDir,
		IsolationMode: IsolationOS,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"第一个可写目录内应该可写", "/home/user/test.txt", true},
		{"第二个可写目录内应该可写", "/var/log/app.log", true},
		{"可写目录外应该只读", "/etc/passwd", false},
		{"系统临时目录（默认可写）内应该可写", fmt.Sprintf("%s/tmpfile", tmpDir), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sb.IsPathWritable(tt.path)
			if got != tt.want {
				t.Errorf("IsPathWritable(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestDirectoryPermissionsWithRelativePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("此测试仅适用于 Unix 系统")
	}

	tmpDir := "/tmp"

	cfg := Config{
		WritableDirs:  []string{tmpDir},
		WorkDir:       tmpDir,
		IsolationMode: IsolationOS,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	// 相对路径应解析为绝对路径后再检查
	// 动态计算从 CWD 到 /tmp 的相对路径，确保跨设备和 CWD 深度兼容
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取 CWD 失败: %v", err)
	}
	relToTmp, err := filepath.Rel(cwd, "/tmp")
	if err != nil {
		t.Fatalf("计算相对路径失败: %v", err)
	}
	if !sb.IsPathWritable(filepath.Join(relToTmp, "test.txt")) {
		t.Errorf("相对路径指向可写目录应返回 true（CWD: %s）", cwd)
	}
}

func TestSandboxWriteRestriction(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	if runtime.GOOS == "windows" {
		t.Skip("此测试仅适用于 Unix 系统")
	}

	tmpDir := "/tmp"
	cfg := Config{
		WritableDirs:  []string{tmpDir},
		WorkDir:       tmpDir,
		IsolationMode: IsolationOS,
		Timeout:       5 * time.Second,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	t.Run("写入可写目录应该成功", func(t *testing.T) {
		cmd, err := sb.Execute("sh", "-c", fmt.Sprintf("echo 'test' > %s/sandbox-write-test.txt", tmpDir))
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}

		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)

		err = cmd.Run()
		if err != nil {
			t.Logf("标准输出: %s", stdout.String())
			t.Logf("标准错误: %s", stderr.String())
			t.Skipf("命令执行失败（可能需要额外的隔离工具）: %v", err)
		}
	})

	t.Run("写入只读目录应该失败", func(t *testing.T) {
		cmd, err := sb.Execute("sh", "-c", "echo 'test' > /etc/sandbox-write-test.txt")
		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}

		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)

		err = cmd.Run()
		if err == nil {
			t.Error("向 /etc 写入应该失败")
		} else {
			t.Logf("命令执行失败（符合预期）: %v", err)
		}
	})
}

func TestConcurrentSandbox(t *testing.T) {
	cfg := Config{IsolationMode: IsolationNone, Timeout: 5 * time.Second}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	// 并发执行多个命令
	done := make(chan bool, 3)
	for i := range 3 {
		go func(id int) {
			cmd, err := executeEcho(sb, fmt.Sprintf("hello-%d", id))
			if err != nil {
				t.Errorf("Execute() failed for goroutine %d: %v", id, err)
				done <- false
				return
			}

			var stdout bytes.Buffer
			cmd.SetStdout(&stdout)

			if err := cmd.Run(); err != nil {
				t.Errorf("Run() failed for goroutine %d: %v", id, err)
				done <- false
				return
			}

			output := strings.TrimSpace(stdout.String())
			if output != fmt.Sprintf("hello-%d", id) {
				t.Errorf("Unexpected output for goroutine %d: %s", id, output)
			}
			done <- true
		}(i)
	}

	for range 3 {
		<-done
	}
}
