//go:build windows

package windows

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCommand 测试最基础的命令执行
func TestCommand(t *testing.T) {
	expected := "Hello Windows API"
	// 使用 cmd /c echo 输出字符串
	cmd := Command("cmd", "/c", "echo", expected)

	dir, err := os.MkdirTemp("", "sandbox-acl-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)
	cmd.Dir = dir
	err = cmd.Run()
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}

}

// TestCommandEcho 测试最基础的命令执行和输出抓取
func TestCommandEcho(t *testing.T) {
	expected := "Hello Windows API"
	// 使用 cmd /c echo 输出字符串
	cmd := Command("cmd", "/c", "echo", expected)

	dir, err := os.MkdirTemp("", "sandbox-acl-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}

	got := strings.Trim(strings.TrimSpace(string(output)), "\"") // win下echo命令的离谱行为
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestEnv 测试环境变量是否正确传递
func TestEnv(t *testing.T) {
	cmd := Command("cmd", "/c", "echo %MY_VAR%")
	cmd.Env = []string{"MY_VAR=Gopher"}

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}

	got := strings.TrimSpace(string(output))
	if got != "Gopher" {
		t.Errorf("expected 'Gopher', got %q", got)
	}
}

// TestDir 测试工作目录切换是否生效
func TestDir(t *testing.T) {
	tempDir := os.TempDir()
	// Windows 的 Temp 可能返回短路径或带不同斜杠，转为绝对路径对比
	absTempDir, _ := filepath.Abs(tempDir)

	cmd := Command("cmd", "/c", "cd")
	cmd.Dir = absTempDir

	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}

	got := strings.TrimSpace(string(output))
	// 转换为绝对路径进行不区分大小写的对比（Windows 特性）
	gotAbs, _ := filepath.Abs(got)
	if !strings.EqualFold(gotAbs, absTempDir) {
		t.Errorf("expected dir %q, got %q", absTempDir, gotAbs)
	}
}

// TestStdinPipe 测试标准输入管道
func TestStdinPipe(t *testing.T) {
	// 使用 powershell 的 $input 获取所有输入，它在收到 EOF 时会立即结束
	cmd := Command("powershell", "-Command", "$input")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// 在独立协程中写入
	inputData := "Hello from Stdin"
	go func() {
		defer stdin.Close() // 必须关闭，否则子进程永远不结束
		io.WriteString(stdin, inputData)
	}()

	// Wait 应该在 stdin.Close() 后迅速返回
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Command finished with error: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Test timed out: StdinPipe stuck (possible handle leak)")
	}

	got := strings.TrimSpace(stdout.String())
	if got != inputData {
		t.Errorf("expected %q, got %q", inputData, got)
	}
}

// TestExitError 测试非零退出码的捕捉
func TestExitError(t *testing.T) {
	// 执行一个肯定会失败的命令 (exit 1)
	cmd := Command("cmd", "/c", "exit 1")
	err := cmd.Run()

	if err == nil {
		t.Fatal("expected error but got nil")
	}

	// 验证是否能转换回 exec.ExitError
	if _, ok := err.(interface{ ExitCode() int }); !ok {
		t.Errorf("error should provide ExitCode, got %T", err)
	}
}

// TestLargeOutput 测试异步 IO 拷贝（大批量数据输出）
func TestLargeOutput(t *testing.T) {
	// 产生 1MB 的输出，验证 io.Copy 协程和管道缓冲区处理
	script := "for ($i=0; $i -lt 10000; $i++) { Write-Output '==Line==' }"
	cmd := Command("powershell", "-Command", script)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	err := cmd.Run()
	if err != nil {
		t.Fatalf("Command failed: %v", err)
	}

	if stdout.Len() < 100000 {
		t.Errorf("output too small, got %d bytes", stdout.Len())
	}
}

// TestCombinedOutputOverlap 测试 Stdout 和 Stderr 指向同一个 Writer
func TestCombinedOutputOverlap(t *testing.T) {
	// 向 stdout 输出 "out"，向 stderr 输出 "err"
	cmd := Command("cmd", "/c", "echo out & echo err >&2")

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	err := cmd.Run()
	if err != nil {
		t.Fatal(err)
	}

	res := combined.String()
	if !strings.Contains(res, "out") || !strings.Contains(res, "err") {
		t.Errorf("combined output missing data: %q", res)
	}
}

// TestArgEscaping 测试 Windows 复杂的命令行参数转义
// 验证空格、引号、反斜杠是否被正确处理
func TestArgEscaping(t *testing.T) {
	// 准备一些极其刁钻的参数
	args := []string{
		`with space`,
		`"quoted"`,
		`back\slash`,
		`double\\"slash`,
		`complex "arg" with \backslash\`,
	}

	for _, arg := range args {
		// 使用 PowerShell 直接打印原始参数，验证接收到的是否一致
		cmd := Command("powershell", "-Command", fmt.Sprintf("Write-Output '%s'", arg))
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("failed to escape arg [%s]: %v", arg, err)
			continue
		}

		got := strings.TrimSpace(string(out))
		if got != arg {
			t.Errorf("Escaping failed.\nExp: [%s]\nGot: [%s]", arg, got)
		}
	}
}

// TestFileRedirection 测试直接使用 *os.File 作为输入输出
// 这样可以绕过内部的 io.Copy 协程，走原生的句柄继承
func TestFileRedirection(t *testing.T) {
	tmpFile := filepath.Join(os.TempDir(), "test_redir.txt")
	defer os.Remove(tmpFile)

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	// 运行命令，将输出直接重定向到文件
	cmd := Command("cmd", "/c", "echo FileRedirectTest")
	cmd.Stdout = f
	err = cmd.Run()
	f.Close() // 必须关闭才能读取内容

	if err != nil {
		t.Fatal(err)
	}

	content, _ := os.ReadFile(tmpFile)
	if !strings.Contains(string(content), "FileRedirectTest") {
		t.Errorf("file redirection failed, content: %q", string(content))
	}
}

// TestProcessKill 测试进程启动后的强杀功能
// 验证 c.Process (os.Process) 是否正确绑定
func TestProcessKill(t *testing.T) {
	// 启动一个长时运行的进程 (ping 默认 4 次，足够我们 Kill)
	cmd := Command("ping", "127.0.0.1", "-n", "10")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// 确保进程已经跑起来了
	time.Sleep(500 * time.Millisecond)

	// 调用封装的 os.Process.Kill
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill process: %v", err)
	}

	err := cmd.Wait()
	if err == nil {
		t.Fatal("expected error after kill, but got nil")
	}
	// 在 Windows 上，被强杀的进程通常返回 exit status 1 或特定的系统错误码
}

// TestInheritEnv 测试不设置 Env 字段时，是否能正确继承当前进程的环境变量
func TestInheritEnv(t *testing.T) {
	key := "GO_WIN_TEST_KEY"
	val := "InheritSuccess"
	os.Setenv(key, val)
	defer os.Unsetenv(key)

	// 不设置 cmd.Env
	cmd := Command("cmd", "/c", fmt.Sprintf("echo %%%s%%", key))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), val) {
		t.Errorf("Environment inheritance failed, got: %q", string(out))
	}
}

// TestLookPath 测试自动路径查找
// 验证 Command("notepad") 是否能找到 C:\Windows\System32\notepad.exe
func TestLookPath(t *testing.T) {
	// notepad 是 Windows 必备的
	cmd := Command("notepad")
	err := cmd.Start()
	if err != nil {
		t.Fatalf("failed to start notepad via LookPath: %v", err)
	}

	// 立即杀掉，我们只测试能不能找到并启动
	cmd.Process.Kill()
	cmd.Wait()

	if !filepath.IsAbs(cmd.Path) {
		t.Errorf("cmd.Path should be absolute after LookPath, got: %q", cmd.Path)
	}
}

// TestDoubleStartError 测试防止重复启动同一个 Cmd 实例
func TestDoubleStartError(t *testing.T) {
	cmd := Command("cmd", "/c", "echo 1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	err := cmd.Start()
	if err == nil || !strings.Contains(err.Error(), "already started") {
		t.Errorf("expected 'already started' error, got: %v", err)
	}

	cmd.Wait()
}

// TestStderrCaptureInOutput 验证 Output() 在执行失败时是否正确填充了 ExitError.Stderr
func TestStderrCaptureInOutput(t *testing.T) {
	// 执行一个会向 stderr 报错的命令
	// dir /Z 是一个无效参数
	cmd := Command("cmd", "/c", "dir /Z")
	_, err := cmd.Output()

	if err == nil {
		t.Fatal("expected error for invalid command, got nil")
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		if len(exitErr.Stderr) == 0 {
			t.Error("Output() should have captured Stderr into ExitError")
		} else {
			t.Logf("Captured stderr: %s", string(exitErr.Stderr))
		}
	} else {
		t.Errorf("expected *exec.ExitError, got %T", err)
	}
}

// TestLargeEnvironmentBlock 测试超大环境变量块的处理
func TestLargeEnvironmentBlock(t *testing.T) {
	var bigEnv []string
	for i := 0; i < 100; i++ {
		bigEnv = append(bigEnv, fmt.Sprintf("VAR_%d=%s", i, strings.Repeat("A", 100)))
	}

	cmd := Command("cmd", "/c", "set")
	cmd.Env = bigEnv
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(out), "VAR_99=") {
		t.Error("large environment block might be truncated")
	}
}
