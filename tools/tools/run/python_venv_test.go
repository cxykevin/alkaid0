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
)

func TestPythonVenvInitialization(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_PYTHON") != "true" {
		t.Skip("ALKAID0_TEST_PYTHON not enabled")
	}

	if os.Getenv("ALKAID0_TEST_PYTHON_ONLINE") != "true" {
		t.Skip("ALKAID0_TEST_PYTHON_ONLINE not enabled (requires network for pip install)")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	cfg := cfgstructs.PythonConfig{
		Path:   "", // 自动查找 python3/python
		Source: "", // 使用默认 pip 源
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 初始化 venv 并安装 ipython、openai
	if err := pythonenv.Initialize(ctx, cfg, configPath); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	venvDir := pythonenv.VenvDir()
	if venvDir == "" {
		t.Fatal("VenvDir returned empty after successful initialization")
	}

	expectedVenvDir := filepath.Join(tmpDir, "venv")
	if venvDir != expectedVenvDir {
		t.Errorf("expected venv at %s, got %s", expectedVenvDir, venvDir)
	}

	// 验证 venv 目录存在
	if info, err := os.Stat(venvDir); err != nil {
		t.Errorf("venv directory does not exist: %v", err)
	} else if !info.IsDir() {
		t.Error("venv path is not a directory")
	}

	// 验证 Python 解释器存在
	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		t.Fatal("VenvPython returned empty")
	}

	if _, err := os.Stat(venvPython); err != nil {
		t.Errorf("venv python interpreter does not exist at %s: %v", venvPython, err)
	}

	// 验证可以执行 Python
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	cmd := pythonenv.TestCommand(ctx2, venvPython, "-c", "print('hello')")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("failed to execute venv python: %v, output: %s", err, output)
	}
	if !strings.Contains(string(output), "hello") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestPythonVenvPackageInstallation(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_PYTHON") != "true" {
		t.Skip("ALKAID0_TEST_PYTHON not enabled")
	}

	if os.Getenv("ALKAID0_TEST_PYTHON_ONLINE") != "true" {
		t.Skip("ALKAID0_TEST_PYTHON_ONLINE not enabled (requires network for pip install)")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	cfg := cfgstructs.PythonConfig{
		Path:   "",
		Source: "",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := pythonenv.Initialize(ctx, cfg, configPath); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		t.Fatal("VenvPython returned empty")
	}

	// 验证 ipython 已安装
	t.Run("ipython installed", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := pythonenv.TestCommand(ctx, venvPython, "-m", "pip", "show", "ipython")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("ipython not installed: %v, output: %s", err, output)
		}
		if !strings.Contains(string(output), "Name: ipython") {
			t.Errorf("unexpected pip show output for ipython: %s", output)
		}
	})

	// 验证 openai 已安装
	t.Run("openai installed", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := pythonenv.TestCommand(ctx, venvPython, "-m", "pip", "show", "openai")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("openai not installed: %v, output: %s", err, output)
		}
		if !strings.Contains(string(output), "Name: openai") {
			t.Errorf("unexpected pip show output for openai: %s", output)
		}
	})

	// 验证可以导入 openai
	t.Run("import openai", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := pythonenv.TestCommand(ctx, venvPython, "-c", "import openai; print('import success')")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("failed to import openai: %v, output: %s", err, output)
		}
		if !strings.Contains(string(output), "import success") {
			t.Errorf("unexpected output: %s", output)
		}
	})
}

func TestPythonVenvWithCustomSource(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_PYTHON") != "true" {
		t.Skip("ALKAID0_TEST_PYTHON not enabled")
	}

	if os.Getenv("ALKAID0_TEST_PYTHON_ONLINE") != "true" {
		t.Skip("ALKAID0_TEST_PYTHON_ONLINE not enabled (requires network)")
	}

	// 使用清华镜像源
	customSource := "https://pypi.tuna.tsinghua.edu.cn/simple"

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to create config file: %v", err)
	}

	cfg := cfgstructs.PythonConfig{
		Path:   "",
		Source: customSource,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := pythonenv.Initialize(ctx, cfg, configPath); err != nil {
		t.Fatalf("Initialize with custom source failed: %v", err)
	}

	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		t.Fatal("VenvPython returned empty")
	}

	// 验证包已安装（证明自定义源生效）
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	cmd := pythonenv.TestCommand(ctx2, venvPython, "-m", "pip", "show", "ipython")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("ipython not installed with custom source: %v, output: %s", err, output)
	}
	if !strings.Contains(string(output), "Name: ipython") {
		t.Errorf("unexpected output: %s", output)
	}
}
