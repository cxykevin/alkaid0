package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cfgstructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/terminal/pythonenv"
	"github.com/cxykevin/alkaid0/terminal/sandbox"
)

// setupTestVenv 初始化测试用的 Python venv（如果尚未初始化）
func setupTestVenv(t *testing.T) string {
	if os.Getenv("ALKAID0_TEST_PYTHON") != "true" {
		t.Skip("ALKAID0_TEST_PYTHON not enabled")
	}

	venvDir := pythonenv.VenvDir()
	if venvDir != "" {
		// 已经初始化
		return venvDir
	}

	// 需要初始化 venv
	tmpConfigDir := t.TempDir()
	tmpConfigPath := filepath.Join(tmpConfigDir, "config.json")
	if err := os.WriteFile(tmpConfigPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to create temp config: %v", err)
	}

	cfg := cfgstructs.PythonConfig{
		Path:   "", // 自动查找
		Source: "", // 默认源
	}

	// 在线测试时使用真实 pip 安装
	if os.Getenv("ALKAID0_TEST_PYTHON_ONLINE") == "true" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if err := pythonenv.Initialize(ctx, cfg, tmpConfigPath); err != nil {
			t.Fatalf("pythonenv.Initialize failed: %v", err)
		}
	} else {
		// 离线测试：只创建 venv，不安装包
		t.Skip("Python venv not initialized and ALKAID0_TEST_PYTHON_ONLINE not enabled")
	}

	venvDir = pythonenv.VenvDir()
	if venvDir == "" {
		t.Fatal("venv initialization succeeded but VenvDir() returned empty")
	}

	return venvDir
}

func TestPythonExecutionInSandbox(t *testing.T) {
	setupTestVenv(t)

	if !sandbox.IsSandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}

	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		t.Fatal("python venv not initialized")
	}

	tests := []struct {
		name        string
		code        string
		expectError bool
		checkOutput func(t *testing.T, output string)
	}{
		{
			name: "simple print",
			code: "print('Hello from sandbox')",
			checkOutput: func(t *testing.T, output string) {
				if !strings.Contains(output, "Hello from sandbox") {
					t.Errorf("expected output to contain 'Hello from sandbox', got: %s", output)
				}
			},
		},
		{
			name: "arithmetic",
			code: "result = 2 + 3\nprint(f'Result: {result}')",
			checkOutput: func(t *testing.T, output string) {
				if !strings.Contains(output, "Result: 5") {
					t.Errorf("expected 'Result: 5', got: %s", output)
				}
			},
		},
		{
			name: "import standard library",
			code: "import math\nprint(math.pi)",
			checkOutput: func(t *testing.T, output string) {
				if !strings.Contains(output, "3.14") {
					t.Errorf("expected pi value, got: %s", output)
				}
			},
		},
		{
			name: "multiline script",
			code: `
def greet(name):
    return f"Hello, {name}!"

for i in range(3):
    print(greet(f"User{i}"))
`,
			checkOutput: func(t *testing.T, output string) {
				if !strings.Contains(output, "Hello, User0!") ||
					!strings.Contains(output, "Hello, User1!") ||
					!strings.Contains(output, "Hello, User2!") {
					t.Errorf("expected all greetings, got: %s", output)
				}
			},
		},
		{
			name:        "syntax error",
			code:        "print('unclosed string",
			expectError: true,
		},
		{
			name:        "runtime error",
			code:        "x = 1 / 0",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			tmpDir := t.TempDir()
			venvDir := pythonenv.VenvDir()

			req := &Request{
				SessionID:        1,
				AgentID:          "",
				ToolID:           "test",
				Reason:           "test python in sandbox",
				Program:          venvPython,
				Args:             []string{"-c", tt.code},
				Stdin:            "",
				DisplayCommand:   "python (test)",
				Env:              os.Environ(),
				WorkDir:          tmpDir,
				Timeout:          5 * time.Second,
				Sandbox:          true,
				SandboxSpecified: true,
				WritableDirs:     []string{venvDir},
			}

			job, err := Default.Submit(ctx, req)
			if err != nil {
				t.Fatalf("Submit failed: %v", err)
			}

			result := job.Wait(ctx)

			if result.CreateErr != nil {
				t.Fatalf("CreateErr: %v", result.CreateErr)
			}

			if tt.expectError {
				if result.Success {
					t.Errorf("expected error, but command succeeded")
				}
			} else {
				if !result.Success {
					t.Errorf("command failed: %s%s", result.ErrString, result.Output)
				}
				if tt.checkOutput != nil {
					tt.checkOutput(t, result.Output)
				}
			}
		})
	}
}

func TestPythonSandboxFileAccess(t *testing.T) {
	setupTestVenv(t)

	if !sandbox.IsSandboxSupported() {
		t.Skip("sandbox not supported on this platform")
	}

	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		t.Fatal("python venv not initialized")
	}

	tmpDir := t.TempDir()
	venvDir := pythonenv.VenvDir()

	// 在工作目录创建测试文件
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	tests := []struct {
		name         string
		code         string
		writableDirs []string
		expectError  bool
		checkOutput  func(t *testing.T, output string)
	}{
		{
			name: "read file in workdir",
			code: `
with open('test.txt', 'r') as f:
    print(f.read())
`,
			writableDirs: []string{venvDir, tmpDir},
			checkOutput: func(t *testing.T, output string) {
				if !strings.Contains(output, "test content") {
					t.Errorf("expected 'test content', got: %s", output)
				}
			},
		},
		{
			name: "write file in workdir",
			code: `
with open('output.txt', 'w') as f:
    f.write('written from python')
print('write successful')
`,
			writableDirs: []string{venvDir, tmpDir},
			checkOutput: func(t *testing.T, output string) {
				if !strings.Contains(output, "write successful") {
					t.Errorf("expected 'write successful', got: %s", output)
				}
				content, err := os.ReadFile(filepath.Join(tmpDir, "output.txt"))
				if err != nil {
					t.Errorf("failed to read output.txt: %v", err)
					return
				}
				if string(content) != "written from python" {
					t.Errorf("expected 'written from python', got: %s", content)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req := &Request{
				SessionID:        1,
				AgentID:          "",
				ToolID:           "test",
				Reason:           "test sandbox file access",
				Program:          venvPython,
				Args:             []string{"-c", tt.code},
				Stdin:            "",
				DisplayCommand:   "python (test file access)",
				Env:              os.Environ(),
				WorkDir:          tmpDir,
				Timeout:          5 * time.Second,
				Sandbox:          true,
				SandboxSpecified: true,
				WritableDirs:     tt.writableDirs,
			}

			job, err := Default.Submit(ctx, req)
			if err != nil {
				t.Fatalf("Submit failed: %v", err)
			}

			result := job.Wait(ctx)

			if result.CreateErr != nil {
				t.Fatalf("CreateErr: %v", result.CreateErr)
			}

			if tt.expectError {
				if result.Success {
					t.Errorf("expected error, but command succeeded")
				}
			} else {
				if !result.Success {
					t.Errorf("command failed: %s%s", result.ErrString, result.Output)
				}
				if tt.checkOutput != nil {
					tt.checkOutput(t, result.Output)
				}
			}
		})
	}
}

func TestPythonTimeout(t *testing.T) {
	setupTestVenv(t)

	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		t.Fatal("python venv not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	venvDir := pythonenv.VenvDir()

	// 一个会超时的脚本
	code := `
import time
time.sleep(10)
print('should not reach here')
`

	req := &Request{
		SessionID:        1,
		AgentID:          "",
		ToolID:           "test",
		Reason:           "test timeout",
		Program:          venvPython,
		Args:             []string{"-c", code},
		Stdin:            "",
		DisplayCommand:   "python (test timeout)",
		Env:              os.Environ(),
		WorkDir:          tmpDir,
		Timeout:          1 * time.Second, // 1秒超时
		Sandbox:          false,
		SandboxSpecified: true,
		WritableDirs:     []string{venvDir},
	}

	job, err := Default.Submit(ctx, req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	result := job.Wait(ctx)

	if result.CreateErr != nil {
		t.Fatalf("CreateErr: %v", result.CreateErr)
	}

	// 应该因超时失败
	if result.Success {
		t.Errorf("expected timeout failure, but command succeeded")
	}

	if !strings.Contains(result.Output, "should not reach here") {
		// 超时应该在打印前中断
		t.Logf("command timed out as expected, output: %s", result.Output)
	}
}

func TestPythonCancellation(t *testing.T) {
	setupTestVenv(t)

	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		t.Fatal("python venv not initialized")
	}

	ctx, cancel := context.WithCancel(context.Background())

	tmpDir := t.TempDir()
	venvDir := pythonenv.VenvDir()

	code := `
import time
for i in range(10):
    print(f"iteration {i}")
    time.sleep(1)
`

	req := &Request{
		SessionID:        1,
		AgentID:          "",
		ToolID:           "test",
		Reason:           "test cancellation",
		Program:          venvPython,
		Args:             []string{"-c", code},
		Stdin:            "",
		DisplayCommand:   "python (test cancel)",
		Env:              os.Environ(),
		WorkDir:          tmpDir,
		Timeout:          30 * time.Second,
		Sandbox:          false,
		SandboxSpecified: true,
		WritableDirs:     []string{venvDir},
	}

	job, err := Default.Submit(ctx, req)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// 等待一点时间后取消
	time.Sleep(500 * time.Millisecond)
	cancel()

	result := job.Wait(context.Background())

	if result.CreateErr != nil {
		t.Fatalf("CreateErr: %v", result.CreateErr)
	}

	// 应该被中断
	if result.Success {
		t.Errorf("expected cancellation, but command succeeded")
	}

	if result.Killed {
		t.Logf("command was killed as expected")
	}

	// 不应该完成所有迭代
	iterationCount := strings.Count(result.Output, "iteration")
	if iterationCount >= 10 {
		t.Errorf("expected fewer than 10 iterations due to cancellation, got %d", iterationCount)
	}
	t.Logf("completed %d iterations before cancellation", iterationCount)
}
