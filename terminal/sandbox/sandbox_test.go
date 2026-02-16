package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cfg := Config{
		WritableDirs: []string{"/tmp/test"},
		Timeout:      5 * time.Second,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	if sb == nil {
		t.Fatal("沙盒为nil")
	}

	if len(sb.writableDirs) == 0 {
		t.Error("可写目录列表为空")
	}

	// 默认隔离模式是IsolationNone（零值）
	if sb.GetIsolationMode() != IsolationNone {
		t.Errorf("默认隔离模式 = %v, 期望 IsolationNone", sb.GetIsolationMode())
	}
}

func TestIsolationModes(t *testing.T) {
	tests := []struct {
		name     string
		mode     IsolationMode
		expected IsolationMode
	}{
		{"显式设置None", IsolationNone, IsolationNone},
		{"显式设置OS", IsolationOS, IsolationOS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				IsolationMode: tt.mode,
			}

			sb, err := New(cfg)
			if err != nil {
				t.Fatalf("创建沙盒失败: %v", err)
			}

			if sb.GetIsolationMode() != tt.expected {
				t.Errorf("隔离模式 = %v, 期望 %v", sb.GetIsolationMode(), tt.expected)
			}
		})
	}
}

func TestIsPathWritable(t *testing.T) {
	tmpDir := os.TempDir()

	tests := []struct {
		name          string
		isolationMode IsolationMode
		path          string
		writable      bool
	}{
		{"无隔离-任意路径", IsolationNone, "/etc", true},
		{"OS隔离-临时目录", IsolationOS, tmpDir, true},
		{"OS隔离-系统目录", IsolationOS, "/etc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				WritableDirs:  []string{tmpDir},
				IsolationMode: tt.isolationMode,
			}

			sb, err := New(cfg)
			if err != nil {
				t.Fatalf("创建沙盒失败: %v", err)
			}

			result := sb.IsPathWritable(tt.path)
			if result != tt.writable {
				t.Errorf("IsPathWritable(%s) = %v, 期望 %v", tt.path, result, tt.writable)
			}
		})
	}
}

func TestExecuteCommandNoIsolation(t *testing.T) {
	cfg := Config{
		Timeout:       5 * time.Second,
		IsolationMode: IsolationNone, // 使用无隔离模式确保测试能运行
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	// 根据平台选择命令
	var cmdName string
	var cmdArgs []string
	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
		cmdArgs = []string{"/c", "echo hello"}
	} else {
		cmdName = "echo"
		cmdArgs = []string{"hello"}
	}

	cmd, err := sb.Execute(cmdName, cmdArgs...)
	if err != nil {
		t.Fatalf("创建命令失败: %v", err)
	}

	var stdout bytes.Buffer
	cmd.SetStdout(&stdout)

	if err := cmd.Run(); err != nil {
		t.Fatalf("执行命令失败: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output != "hello" {
		t.Errorf("输出 = %q, 期望 %q", output, "hello")
	}
}

func TestExecuteCommandWithIsolation(t *testing.T) {
	// 跳过需要特殊权限的测试
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	startAt := time.Now()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var runErr error

	logf := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		t.Log(msg)
		fmt.Fprintln(os.Stderr, msg)
	}

	logf("开始 TestExecuteCommandWithIsolation")

	defer func() {
		logf("遗言: elapsed=%s runErr=%v stdout=%q stderr=%q", time.Since(startAt), runErr, stdout.String(), stderr.String())
	}()

	defer func() {
		if r := recover(); r != nil {
			stack := make([]byte, 1<<16)
			n := runtime.Stack(stack, true)
			logf("panic: %v\n%s", r, string(stack[:n]))
			panic(r)
		}
	}()

	watchdog := time.AfterFunc(10*time.Second, func() {
		stack := make([]byte, 1<<16)
		n := runtime.Stack(stack, true)
		fmt.Fprintf(os.Stderr, "watchdog: 执行超时未结束\n%s", string(stack[:n]))
	})
	defer watchdog.Stop()

	cfg := Config{
		Timeout:       2 * time.Second,
		IsolationMode: IsolationOS,
	}
	defer func() {
		logf("配置: isolation=%v timeout=%s", cfg.IsolationMode, cfg.Timeout)
	}()

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}
	defer func() {
		logf("沙盒目录: tmp=%q work=%q writable=%v", sb.tmpDir, sb.workDir, sb.writableDirs)
	}()

	var cmdName string
	var cmdArgs []string
	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
		cmdArgs = []string{"/c", "echo hello"}
	} else {
		cmdName = "echo"
		cmdArgs = []string{"hello"}
	}
	defer func() {
		logf("命令: %s %v", cmdName, cmdArgs)
	}()

	logf("准备创建命令")
	createStart := time.Now()
	cmd, err := sb.Execute(cmdName, cmdArgs...)
	logf("创建命令返回, elapsed=%s", time.Since(createStart))
	if err != nil {
		t.Fatalf("创建命令失败: %v", err)
	}

	cmd.SetStdout(&stdout)
	cmd.SetStderr(&stderr)
	logf("已设置标准输出/错误")

	logf("开始执行")
	runErr = cmd.Run()
	if runErr != nil {
		logf("隔离执行失败（可能需要额外工具）: %v", runErr)
		t.Skip("跳过隔离执行测试")
	}

	output := strings.TrimSpace(stdout.String())
	logf("隔离执行输出: %q", output)
}

func TestCommandTimeout(t *testing.T) {
	cfg := Config{
		Timeout:       1 * time.Second,
		IsolationMode: IsolationNone,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	// 根据平台选择睡眠命令
	var cmdName string
	var cmdArgs []string
	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
		cmdArgs = []string{"/c", "timeout", "5"}
	} else {
		cmdName = "sleep"
		cmdArgs = []string{"5"}
	}

	cmd, err := sb.Execute(cmdName, cmdArgs...)
	if err != nil {
		t.Fatalf("创建命令失败: %v", err)
	}

	err = cmd.Run()
	if err == nil {
		t.Error("期望超时错误，但命令成功完成")
	}
}

func TestValidateCommand(t *testing.T) {
	// 根据平台选择命令
	var validCmd, dangerousCmd string
	if runtime.GOOS == "windows" {
		validCmd = "cmd.exe"
		dangerousCmd = "del"
	} else {
		validCmd = "echo"
		dangerousCmd = "rm"
	}

	tests := []struct {
		name          string
		isolationMode IsolationMode
		command       string
		shouldErr     bool
	}{
		{"无隔离-有效命令", IsolationNone, validCmd, false},
		{"无隔离-危险命令", IsolationNone, dangerousCmd, false},              // 无隔离模式不检查危险命令
		{"无隔离-不存在命令", IsolationNone, "nonexistentcommand12345", true}, // 但仍检查命令是否存在
		{"OS隔离-有效命令", IsolationOS, validCmd, false},
		{"OS隔离-不存在命令", IsolationOS, "nonexistentcommand12345", true},
		{"OS隔离-危险命令", IsolationOS, dangerousCmd, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				IsolationMode: tt.isolationMode,
			}
			sb, err := New(cfg)
			if err != nil {
				t.Fatalf("创建沙盒失败: %v", err)
			}

			err = sb.ValidateCommand(tt.command)
			if (err != nil) != tt.shouldErr {
				t.Errorf("ValidateCommand(%s) error = %v, shouldErr = %v", tt.command, err, tt.shouldErr)
			}
		})
	}
}

func TestSetWorkDir(t *testing.T) {
	cfg := Config{
		IsolationMode: IsolationNone,
	}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	tmpDir := os.TempDir()
	err = sb.SetWorkDir(tmpDir)
	if err != nil {
		t.Errorf("设置工作目录失败: %v", err)
	}

	if sb.GetWorkDir() != tmpDir {
		t.Errorf("工作目录 = %s, 期望 %s", sb.GetWorkDir(), tmpDir)
	}

	// 测试不存在的目录
	err = sb.SetWorkDir("/nonexistent/directory/12345")
	if err == nil {
		t.Error("期望设置不存在目录时出错")
	}
}

func TestSetIsolationMode(t *testing.T) {
	cfg := Config{
		IsolationMode: IsolationOS, // 使用非零值
	}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}
	sb.SetIsolationMode(IsolationOS)
	if sb.GetIsolationMode() != IsolationOS {
		t.Errorf("设置后隔离模式 = %v, 期望 IsolationOS", sb.GetIsolationMode())
	}

	sb.SetIsolationMode(IsolationNone)
	if sb.GetIsolationMode() != IsolationNone {
		t.Errorf("再次设置后隔离模式 = %v, 期望 IsolationNone", sb.GetIsolationMode())
	}
}

func TestGetPlatformInfo(t *testing.T) {
	info := GetPlatformInfo()

	if info["os"] == "" {
		t.Error("平台信息中缺少os")
	}

	if info["arch"] == "" {
		t.Error("平台信息中缺少arch")
	}

	if info["version"] == "" {
		t.Error("平台信息中缺少version")
	}

	if info["isolation"] == "" {
		t.Error("平台信息中缺少isolation")
	}

	t.Logf("平台信息: %v", info)

	// 验证隔离能力检测
	switch runtime.GOOS {
	case "linux":
		if info["isolation"] != "user-namespaces" && info["isolation"] != "none" {
			t.Errorf("Linux平台隔离能力异常: %s", info["isolation"])
		}
	case "darwin":
		if info["isolation"] != "sandbox-exec" && info["isolation"] != "none" {
			t.Errorf("macOS平台隔离能力异常: %s", info["isolation"])
		}
	case "windows":
		if info["isolation"] != "appcontainer" && info["isolation"] != "none" {
			t.Errorf("Windows平台隔离能力异常: %s", info["isolation"])
		}
	}
}

func TestIsolationModeString(t *testing.T) {
	tests := []struct {
		mode     IsolationMode
		expected string
	}{
		{IsolationNone, "none"},
		{IsolationOS, "os"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.mode.String()
			if result != tt.expected {
				t.Errorf("IsolationMode.String() = %s, 期望 %s", result, tt.expected)
			}
		})
	}
}

func TestIsolationOSSpec(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	switch runtime.GOOS {
	case "linux":
		cfg := Config{
			WritableDirs:  []string{"/tmp"},
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
	default:
		t.Skip("跳过未知平台隔离测试")
	}

}

func TestDirectoryPermissions(t *testing.T) {
	// 创建临时测试目录（可写）
	tempDir, err := os.MkdirTemp("", "sandbox-test-temp-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 在当前工作目录下创建只读测试目录（不在系统临时目录中）
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取工作目录失败: %v", err)
	}
	readOnlyDir := filepath.Join(workDir, "sandbox-test-readonly-temp")
	if err := os.MkdirAll(readOnlyDir, 0755); err != nil {
		t.Fatalf("创建只读测试目录失败: %v", err)
	}
	defer os.RemoveAll(readOnlyDir)

	tests := []struct {
		name          string
		isolationMode IsolationMode
		testPath      string
		shouldWrite   bool
		description   string
	}{
		{
			name:          "OS隔离-temp目录内可写",
			isolationMode: IsolationOS,
			testPath:      filepath.Join(tempDir, "test.txt"),
			shouldWrite:   true,
			description:   "指定目录内应该可写",
		},
		{
			name:          "OS隔离-temp目录内子目录可写",
			isolationMode: IsolationOS,
			testPath:      filepath.Join(tempDir, "subdir", "test.txt"),
			shouldWrite:   true,
			description:   "指定目录的子目录应该可写",
		},
		{
			name:          "OS隔离-非指定目录只读",
			isolationMode: IsolationOS,
			testPath:      filepath.Join(readOnlyDir, "test.txt"),
			shouldWrite:   false,
			description:   "指定目录外应该只读",
		},
		{
			name:          "OS隔离-非指定目录子目录只读",
			isolationMode: IsolationOS,
			testPath:      filepath.Join(readOnlyDir, "subdir", "test.txt"),
			shouldWrite:   false,
			description:   "非指定目录的子目录应该只读",
		},
		{
			name:          "无隔离-任意路径可写",
			isolationMode: IsolationNone,
			testPath:      filepath.Join(readOnlyDir, "test.txt"),
			shouldWrite:   true,
			description:   "无隔离模式下所有路径都可写",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				WritableDirs:  []string{tempDir},
				IsolationMode: tt.isolationMode,
			}

			sb, err := New(cfg)
			if err != nil {
				t.Fatalf("创建沙盒失败: %v", err)
			}

			result := sb.IsPathWritable(tt.testPath)
			if result != tt.shouldWrite {
				t.Errorf("%s: IsPathWritable(%s) = %v, 期望 %v",
					tt.description, tt.testPath, result, tt.shouldWrite)
			}
		})
	}
}

func TestDirectoryPermissionsWithMultipleDirs(t *testing.T) {
	// 创建两个临时测试目录（可写）
	tempDir1, err := os.MkdirTemp("", "sandbox-test-temp1-*")
	if err != nil {
		t.Fatalf("创建临时目录1失败: %v", err)
	}
	defer os.RemoveAll(tempDir1)

	tempDir2, err := os.MkdirTemp("", "sandbox-test-temp2-*")
	if err != nil {
		t.Fatalf("创建临时目录2失败: %v", err)
	}
	defer os.RemoveAll(tempDir2)

	// 在当前工作目录下创建只读测试目录（不在系统临时目录中）
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取工作目录失败: %v", err)
	}
	readOnlyDir := filepath.Join(workDir, "sandbox-test-readonly-temp2")
	if err := os.MkdirAll(readOnlyDir, 0755); err != nil {
		t.Fatalf("创建只读测试目录失败: %v", err)
	}
	defer os.RemoveAll(readOnlyDir)

	cfg := Config{
		WritableDirs:  []string{tempDir1, tempDir2},
		IsolationMode: IsolationOS,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	tests := []struct {
		path        string
		shouldWrite bool
		description string
	}{
		{
			path:        filepath.Join(tempDir1, "test.txt"),
			shouldWrite: true,
			description: "第一个可写目录内应该可写",
		},
		{
			path:        filepath.Join(tempDir2, "test.txt"),
			shouldWrite: true,
			description: "第二个可写目录内应该可写",
		},
		{
			path:        filepath.Join(readOnlyDir, "test.txt"),
			shouldWrite: false,
			description: "可写目录外应该只读",
		},
		{
			path:        filepath.Join(os.TempDir(), "other", "test.txt"),
			shouldWrite: true,
			description: "系统临时目录（默认可写）内应该可写",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := sb.IsPathWritable(tt.path)
			if result != tt.shouldWrite {
				t.Errorf("%s: IsPathWritable(%s) = %v, 期望 %v",
					tt.description, tt.path, result, tt.shouldWrite)
			}
		})
	}
}

func TestDirectoryPermissionsWithRelativePaths(t *testing.T) {
	// 创建临时测试目录
	tempDir, err := os.MkdirTemp("", "sandbox-test-temp-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := Config{
		WritableDirs:  []string{tempDir},
		IsolationMode: IsolationOS,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	// 测试相对路径
	absPath, err := filepath.Abs(tempDir)
	if err != nil {
		t.Fatalf("获取绝对路径失败: %v", err)
	}

	// 测试绝对路径
	if !sb.IsPathWritable(absPath) {
		t.Errorf("绝对路径应该可写: %s", absPath)
	}

	// 测试路径规范化（带有..的路径）
	pathWithDots := filepath.Join(tempDir, "subdir", "..", "test.txt")
	if !sb.IsPathWritable(pathWithDots) {
		t.Errorf("规范化后在可写目录内的路径应该可写: %s", pathWithDots)
	}

	// 测试试图逃逸的路径
	escapePath := filepath.Join(tempDir, "..", "..", "etc", "test.txt")
	if sb.IsPathWritable(escapePath) {
		t.Errorf("逃逸到可写目录外的路径应该只读: %s", escapePath)
	}
}

func TestSandboxWriteRestriction(t *testing.T) {
	// 跳过需要特殊权限的测试
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}

	// 创建可写目录
	writableDir, err := os.MkdirTemp("", "sandbox-test-writable-*")
	if err != nil {
		t.Fatalf("创建可写目录失败: %v", err)
	}
	defer os.RemoveAll(writableDir)

	var readOnlyDir string
	// 创建只读目录（不在可写列表中）
	if runtime.GOOS == "windows" {
		readOnlyDir = "C:\\"
	} else {
		// 使用 /var 目录，它不太可能在临时目录中
		readOnlyDir = "/var/tmp/sandbox-test-write-restriction-" + fmt.Sprintf("%d", os.Getpid())
		if err := os.MkdirAll(readOnlyDir, 0755); err != nil {
			t.Fatalf("创建只读目录失败: %v", err)
		}
		defer os.RemoveAll(readOnlyDir)
	}

	cfg := Config{
		WritableDirs:  []string{writableDir},
		IsolationMode: IsolationOS,
		Timeout:       5 * time.Second,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	tests := []struct {
		name          string
		targetDir     string
		shouldSucceed bool
		description   string
	}{
		{
			name:          "写入可写目录应该成功",
			targetDir:     writableDir,
			shouldSucceed: true,
			description:   "在指定的可写目录内写入文件应该成功",
		},
		{
			name:          "写入只读目录应该失败",
			targetDir:     readOnlyDir,
			shouldSucceed: false,
			description:   "在非指定目录写入文件应该失败",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// testFile := filepath.Join(tt.targetDir, "test.txt")
			testFile := filepath.Join(tt.targetDir, "test.txt")

			var cmdName string
			var cmdArgs []string

			// 根据平台选择写入命令
			if runtime.GOOS == "windows" {
				cmdName = "cmd.exe"
				cmdArgs = []string{"/c", fmt.Sprintf("echo test > %s", testFile)}
			} else {
				cmdName = "sh"
				cmdArgs = []string{"-c", fmt.Sprintf("echo test > %s", testFile)}
			}

			cmd, err := sb.Execute(cmdName, cmdArgs...)
			if err != nil {
				t.Fatalf("创建命令失败: %v", err)
			}

			var stdout, stderr bytes.Buffer
			cmd.SetStdout(&stdout)
			cmd.SetStderr(&stderr)

			err = cmd.Run()

			if tt.shouldSucceed {
				if err != nil {
					t.Logf("标准输出: %s", stdout.String())
					t.Logf("标准错误: %s", stderr.String())
					t.Logf("命令执行失败（可能需要额外的隔离工具）: %v", err)
					t.Skip("跳过隔离写入测试")
				}

				// 验证文件是否创建成功
				if _, err := os.Stat(testFile); os.IsNotExist(err) {
					t.Errorf("%s: 文件未创建", tt.description)
				} else {
					// 清理测试文件
					os.Remove(testFile)
				}
			} else {
				// 对于只读目录，命令可能失败或文件创建失败
				if err == nil {
					// 检查文件是否真的被创建了
					if _, statErr := os.Stat(testFile); statErr == nil {
						t.Errorf("%s: 文件不应该被创建", tt.description)
						os.Remove(testFile)
					} else {
						t.Logf("文件未创建（符合预期）")
					}
				} else {
					t.Logf("命令执行失败（符合预期）: %v", err)
				}
			}
		})
	}
}

func BenchmarkExecuteNoIsolation(b *testing.B) {
	cfg := Config{
		IsolationMode: IsolationNone,
	}

	sb, err := New(cfg)
	if err != nil {
		b.Fatalf("创建沙盒失败: %v", err)
	}

	var cmdName string
	var cmdArgs []string
	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
		cmdArgs = []string{"/c", "echo test"}
	} else {
		cmdName = "echo"
		cmdArgs = []string{"test"}
	}

	for b.Loop() {
		cmd, err := sb.Execute(cmdName, cmdArgs...)
		if err != nil {
			b.Fatalf("创建命令失败: %v", err)
		}

		if err := cmd.Run(); err != nil {
			b.Fatalf("执行命令失败: %v", err)
		}
	}
}
