package sandbox

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestVerifyWriteFile(t *testing.T) {
	// 创建临时目录用于写入测试
	tmpDir, err := os.MkdirTemp("", "sandbox-verify-write-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "test-output.txt")
	testContent := "Hello from sandbox!"

	// 测试无隔离模式
	t.Run("无隔离模式写文件", func(t *testing.T) {
		cfg := Config{
			IsolationMode: IsolationNone,
			Timeout:       5 * time.Second,
		}

		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}

		// 根据平台选择命令
		var cmd *Command
		if runtime.GOOS == "windows" {
			// Windows: 使用 cmd.exe
			cmd, err = sb.Execute("cmd.exe", "/c", fmt.Sprintf("echo %s > %s", testContent, testFile))
		} else {
			// Unix: 使用 sh
			cmd, err = sb.Execute("sh", "-c", fmt.Sprintf("echo '%s' > %s", testContent, testFile))
		}

		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}

		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)

		if err := cmd.Run(); err != nil {
			t.Fatalf("命令执行失败: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}

		// 验证文件是否创建
		content, err := os.ReadFile(testFile)
		if err != nil {
			t.Fatalf("读取文件失败: %v", err)
		}

		expectedContent := testContent + "\n"
		if runtime.GOOS == "windows" {
			// Windows 的 echo 会添加 \r\n
			expectedContent = testContent + " \r\n"
		}

		if string(content) != expectedContent {
			t.Errorf("文件内容不匹配，期望 %q，得到 %q", expectedContent, string(content))
		}

		t.Logf("✓ 无隔离模式写文件成功")
		os.Remove(testFile)
	})

	// 测试OS隔离模式
	t.Run("OS隔离模式写文件", func(t *testing.T) {
		if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
			t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
		}

		cfg := Config{
			WritableDirs:  []string{tmpDir},
			IsolationMode: IsolationOS,
			Timeout:       5 * time.Second,
		}

		sb, err := New(cfg)
		if err != nil {
			t.Fatalf("创建沙盒失败: %v", err)
		}

		testFile2 := filepath.Join(tmpDir, "test-isolated.txt")

		// 根据平台选择命令
		var cmd *Command
		if runtime.GOOS == "windows" {
			// Windows: 使用 cmd.exe
			cmd, err = sb.Execute("cmd.exe", "/c", fmt.Sprintf("echo %s > %s", testContent, testFile2))
		} else {
			// Unix: 使用 sh
			cmd, err = sb.Execute("sh", "-c", fmt.Sprintf("echo '%s' > %s", testContent, testFile2))
		}

		if err != nil {
			t.Fatalf("创建命令失败: %v", err)
		}

		var stdout, stderr bytes.Buffer
		cmd.SetStdout(&stdout)
		cmd.SetStderr(&stderr)

		if err := cmd.Run(); err != nil {
			t.Fatalf("命令执行失败: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}

		// 验证文件是否创建
		content, err := os.ReadFile(testFile2)
		if err != nil {
			t.Fatalf("读取文件失败: %v", err)
		}

		expectedContent := testContent + "\n"
		if runtime.GOOS == "windows" {
			// Windows 的 echo 会添加 \r\n
			expectedContent = testContent + " \r\n"
		}

		if string(content) != expectedContent {
			t.Errorf("文件内容不匹配，期望 %q，得到 %q", expectedContent, string(content))
		}

		t.Logf("✓ OS隔离模式写文件成功")
	})
}
