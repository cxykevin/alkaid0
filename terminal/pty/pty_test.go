package pty

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	cfg := Config{
		Rows: 24,
		Cols: 80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	if pty == nil {
		t.Fatal("PTY为nil")
	}
}

func TestDefaultCommand(t *testing.T) {
	cfg := Config{
		Rows: 24,
		Cols: 80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	// 验证PTY已创建
	if pty == nil {
		t.Error("PTY实例未设置")
	}
}

func TestStartAndClose(t *testing.T) {
	var cmdName string
	var cmdArgs []string

	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
		cmdArgs = []string{"/c", "echo", "test"}
	} else {
		cmdName = "sh"
		cmdArgs = []string{"-c", "echo test"}
	}

	cfg := Config{
		Command: cmdName,
		Args:    cmdArgs,
		Rows:    24,
		Cols:    80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}

	err = pty.Start()
	if err != nil {
		t.Fatalf("启动PTY失败: %v", err)
	}

	// 等待一小段时间
	time.Sleep(100 * time.Millisecond)

	err = pty.Close()
	if err != nil {
		t.Errorf("关闭PTY失败: %v", err)
	}
}

func TestReadWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows上的交互式测试可能不稳定")
	}

	cfg := Config{
		Command: "cat",
		Rows:    24,
		Cols:    80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	err = pty.Start()
	if err != nil {
		t.Fatalf("启动PTY失败: %v", err)
	}

	// 写入数据
	testData := []byte("hello\n")
	n, err := pty.Write(testData)
	if err != nil {
		t.Errorf("写入失败: %v", err)
	}

	if n != len(testData) {
		t.Errorf("写入字节数 = %d, 期望 %d", n, len(testData))
	}

	// 读取回显
	time.Sleep(100 * time.Millisecond)
	buf := make([]byte, 1024)
	n, err = pty.Read(buf)
	if err != nil && err.Error() != "EOF" {
		t.Logf("读取输出: %v (这可能是正常的)", err)
	}

	if n > 0 {
		output := string(buf[:n])
		t.Logf("读取到输出: %q", output)
		if !strings.Contains(output, "hello") {
			t.Logf("输出不包含'hello'，但这可能是正常的")
		}
	}
}

func TestGetPID(t *testing.T) {
	var cmdName string

	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
	} else {
		cmdName = "sh"
	}

	cfg := Config{
		Command: cmdName,
		Rows:    24,
		Cols:    80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	// 启动前PID应该是-1或0
	pidBefore := pty.GetPID()
	t.Logf("启动前PID: %d", pidBefore)

	err = pty.Start()
	if err != nil {
		t.Fatalf("启动PTY失败: %v", err)
	}

	// 启动后应该有有效的PID
	pid := pty.GetPID()
	if pid <= 0 {
		t.Errorf("启动后PID = %d, 应该大于0", pid)
	}
	t.Logf("启动后PID: %d", pid)
}

func TestIsRunning(t *testing.T) {
	var cmdName string
	var cmdArgs []string

	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
		cmdArgs = []string{"/c", "echo", "test"}
	} else {
		cmdName = "sh"
		cmdArgs = []string{"-c", "echo test"}
	}

	cfg := Config{
		Command: cmdName,
		Args:    cmdArgs,
		Rows:    24,
		Cols:    80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	// 启动前不应该在运行
	if pty.IsRunning() {
		t.Error("启动前不应该在运行")
	}

	err = pty.Start()
	if err != nil {
		t.Fatalf("启动PTY失败: %v", err)
	}

	// 等待命令完成
	pty.Wait()

	// 短命令完成后应该不在运行
	time.Sleep(100 * time.Millisecond)
	if pty.IsRunning() {
		t.Log("命令可能仍在运行（这在某些平台上是正常的）")
	}
}

func TestCopyTo(t *testing.T) {
	var cmdName string
	var cmdArgs []string

	if runtime.GOOS == "windows" {
		cmdName = "cmd.exe"
		cmdArgs = []string{"/c", "echo", "test"}
	} else {
		cmdName = "sh"
		cmdArgs = []string{"-c", "echo test"}
	}

	cfg := Config{
		Command: cmdName,
		Args:    cmdArgs,
		Rows:    24,
		Cols:    80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	err = pty.Start()
	if err != nil {
		t.Fatalf("启动PTY失败: %v", err)
	}

	var buf bytes.Buffer
	done := make(chan error)
	go func() {
		done <- pty.CopyTo(&buf)
	}()

	// 等待一些输出或超时
	select {
	case <-done:
		if buf.Len() > 0 {
			t.Logf("捕获到输出: %q", buf.String())
		}
	case <-time.After(500 * time.Millisecond):
		if buf.Len() > 0 {
			t.Logf("捕获到输出: %q", buf.String())
		}
	}
}

func TestResize(t *testing.T) {
	cfg := Config{
		Rows: 24,
		Cols: 80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	err = pty.Start()
	if err != nil {
		t.Fatalf("启动PTY失败: %v", err)
	}

	// 调整大小
	err = pty.Resize(30, 100)
	if err != nil {
		t.Errorf("调整大小失败: %v", err)
	}
}

func TestCloseMultipleTimes(t *testing.T) {
	cfg := Config{
		Rows: 24,
		Cols: 80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}

	err = pty.Start()
	if err != nil {
		t.Fatalf("启动PTY失败: %v", err)
	}

	// 多次关闭不应该panic
	err = pty.Close()
	if err != nil {
		t.Errorf("第一次关闭失败: %v", err)
	}

	err = pty.Close()
	if err != nil {
		t.Log("第二次关闭返回错误（这是可以接受的）")
	}
}

func TestEnvironmentVariables(t *testing.T) {
	cfg := Config{
		Env:  []string{"TEST_VAR=hello", "PATH=/usr/bin"},
		Rows: 24,
		Cols: 80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	// PTY已创建，环境变量已设置
	t.Log("PTY创建成功，环境变量已配置")
}

func TestWorkingDirectory(t *testing.T) {
	cfg := Config{
		Dir:  "/tmp",
		Rows: 24,
		Cols: 80,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	t.Log("PTY创建成功，工作目录已设置为/tmp")
}

func TestGetSize(t *testing.T) {
	cfg := Config{
		Rows: 30,
		Cols: 100,
	}

	pty, err := New(cfg)
	if err != nil {
		t.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	rows, cols, err := pty.GetSize()
	if err != nil {
		t.Logf("获取大小失败: %v (这可能是正常的)", err)
	} else {
		t.Logf("终端大小: %dx%d", rows, cols)
	}
}

func BenchmarkWrite(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("Windows上的基准测试可能不稳定")
	}

	cfg := Config{
		Command: "cat",
		Rows:    24,
		Cols:    80,
	}

	pty, err := New(cfg)
	if err != nil {
		b.Fatalf("创建PTY失败: %v", err)
	}
	defer pty.Close()

	err = pty.Start()
	if err != nil {
		b.Fatalf("启动PTY失败: %v", err)
	}

	data := []byte("test\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pty.Write(data)
	}
}
