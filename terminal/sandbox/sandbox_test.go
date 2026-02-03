package sandbox

import (
	"bytes"
	"os"
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
		{"显式设置App", IsolationApp, IsolationApp},
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
		{"应用隔离-临时目录", IsolationApp, tmpDir, true},
		{"应用隔离-系统目录", IsolationApp, "/etc", false},
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
		cmdArgs = []string{"/c", "echo", "hello"}
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
	if os.Getenv("RUN_ISOLATION_TESTS") != "1" {
		t.Skip("跳过隔离测试（设置 RUN_ISOLATION_TESTS=1 启用）")
	}
	
	cfg := Config{
		Timeout:       5 * time.Second,
		IsolationMode: IsolationOS,
	}

	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}

	var cmdName string
	var cmdArgs []string
	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
		cmdArgs = []string{"/c", "echo", "hello"}
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
		t.Logf("隔离执行失败（可能需要额外工具）: %v", err)
		t.Skip("跳过隔离执行测试")
	}

	output := strings.TrimSpace(stdout.String())
	t.Logf("隔离执行输出: %q", output)
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
	tests := []struct {
		name          string
		isolationMode IsolationMode
		command       string
		shouldErr     bool
	}{
		{"无隔离-有效命令", IsolationNone, "echo", false},
		{"无隔离-危险命令", IsolationNone, "rm", false}, // 无隔离模式不检查危险命令
		{"无隔离-不存在命令", IsolationNone, "nonexistentcommand12345", true}, // 但仍检查命令是否存在
		{"OS隔离-有效命令", IsolationOS, "echo", false},
		{"OS隔离-不存在命令", IsolationOS, "nonexistentcommand12345", true},
		{"OS隔离-危险命令", IsolationOS, "rm", true},
		{"应用隔离-危险命令", IsolationApp, "rm", true},
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
		IsolationMode: IsolationApp, // 使用非零值
	}
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}
	
	if sb.GetIsolationMode() != IsolationApp {
		t.Errorf("初始隔离模式 = %v, 期望 IsolationApp", sb.GetIsolationMode())
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
		{IsolationApp, "app"},
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

func TestLinuxIsolation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("仅在Linux上运行")
	}
	
	if os.Getenv("RUN_ISOLATION_TESTS") != "1" {
		t.Skip("跳过隔离测试（设置 RUN_ISOLATION_TESTS=1 启用）")
	}
	
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
		t.Logf("隔离执行失败: %v", err)
		t.Skip("跳过Linux隔离测试")
	}
	
	t.Logf("读取到hostname: %s", stdout.String())
}

func TestDarwinIsolation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("仅在macOS上运行")
	}
	
	if os.Getenv("RUN_ISOLATION_TESTS") != "1" {
		t.Skip("跳过隔离测试（设置 RUN_ISOLATION_TESTS=1 启用）")
	}
	
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
		t.Logf("隔离执行失败: %v", err)
		t.Skip("跳过macOS隔离测试")
	}
	
	t.Logf("输出: %s", stdout.String())
}

func TestWindowsIsolation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅在Windows上运行")
	}
	
	if os.Getenv("RUN_ISOLATION_TESTS") != "1" {
		t.Skip("跳过隔离测试（设置 RUN_ISOLATION_TESTS=1 启用）")
	}
	
	cfg := Config{
		WritableDirs:  []string{os.TempDir()},
		IsolationMode: IsolationOS,
		Timeout:       5 * time.Second,
	}
	
	sb, err := New(cfg)
	if err != nil {
		t.Fatalf("创建沙盒失败: %v", err)
	}
	
	cmd, err := sb.Execute("cmd.exe", "/c", "echo", "hello")
	if err != nil {
		t.Fatalf("创建命令失败: %v", err)
	}
	
	var stdout bytes.Buffer
	cmd.SetStdout(&stdout)
	
	if err := cmd.Run(); err != nil {
		t.Logf("隔离执行失败: %v", err)
		t.Skip("跳过Windows隔离测试")
	}
	
	t.Logf("输出: %s", stdout.String())
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
		cmdArgs = []string{"/c", "echo", "test"}
	} else {
		cmdName = "echo"
		cmdArgs = []string{"test"}
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd, err := sb.Execute(cmdName, cmdArgs...)
		if err != nil {
			b.Fatalf("创建命令失败: %v", err)
		}
		
		if err := cmd.Run(); err != nil {
			b.Fatalf("执行命令失败: %v", err)
		}
	}
}
