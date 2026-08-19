package pythonenv

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config/structs"
)

func TestInitializeCreatesVenvAndInstallsIPythonOffline(t *testing.T) {
	root := t.TempDir()
	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("python"), 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	var calls [][]string
	oldRunner := commandRunner
	commandRunner = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		if len(calls) == 1 {
			venv := args[len(args)-1]
			venvPython := pythonInVenv(venv)
			if err := os.MkdirAll(filepath.Dir(venvPython), 0755); err != nil {
				return err
			}
			return os.WriteFile(venvPython, []byte("venv python"), 0755)
		}
		if len(calls) == 2 || len(calls) == 4 {
			return os.ErrNotExist
		}
		return nil
	}
	defer func() { commandRunner = oldRunner }()

	if err := Initialize(context.Background(), structs.PythonConfig{Path: python, Source: "https://mirror.invalid/simple"}, configPath); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(root, "venv")
	if got := VenvDir(); got != wantDir {
		t.Fatalf("VenvDir() = %q, want %q", got, wantDir)
	}

	// 验证 ready marker 存在
	markerPath := filepath.Join(wantDir, readyMarkerFile)
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("ready marker file should exist at %s: %v", markerPath, err)
	}

	if len(calls) != 5 {
		t.Fatalf("got %d commands, want 5: %#v", len(calls), calls)
	}
	if !reflect.DeepEqual(calls[0][1:], []string{"-m", "venv", wantDir}) {
		t.Errorf("venv command = %#v", calls[0])
	}
	if !reflect.DeepEqual(calls[1][1:], []string{"-m", "pip", "show", "ipython"}) {
		t.Errorf("ipython pip show command = %#v", calls[1])
	}
	if !reflect.DeepEqual(calls[3][1:], []string{"-m", "pip", "show", "openai"}) {
		t.Errorf("openai pip show command = %#v", calls[3])
	}
	if !strings.HasSuffix(strings.Join(calls[2], " "), "--index-url https://mirror.invalid/simple") {
		t.Errorf("ipython pip install command = %#v", calls[2])
	}
	if !strings.HasSuffix(strings.Join(calls[4], " "), "--index-url https://mirror.invalid/simple") {
		t.Errorf("openai pip install command = %#v", calls[4])
	}
}

func TestInitializeRejectsInvalidExistingVenvWithMarker(t *testing.T) {
	root := t.TempDir()
	venv := filepath.Join(root, "venv")
	if err := os.MkdirAll(venv, 0755); err != nil {
		t.Fatal(err)
	}
	// 写入 ready marker
	markerPath := filepath.Join(venv, readyMarkerFile)
	if err := os.WriteFile(markerPath, []byte("initialized\n"), 0644); err != nil {
		t.Fatal(err)
	}

	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("python"), 0755); err != nil {
		t.Fatal(err)
	}
	oldRunner := commandRunner
	commandRunner = func(context.Context, string, ...string) error { t.Fatal("must not run commands"); return nil }
	defer func() { commandRunner = oldRunner }()

	// 应该失败，因为 venv 有 marker 但无效（没有 Python）
	if err := Initialize(context.Background(), structs.PythonConfig{Path: python}, filepath.Join(root, "config.json")); err == nil {
		t.Fatal("Initialize succeeded for invalid existing venv with marker")
	}
}

func TestInitializeRemovesVenvWithoutMarker(t *testing.T) {
	root := t.TempDir()
	venv := filepath.Join(root, "venv")
	if err := os.MkdirAll(venv, 0755); err != nil {
		t.Fatal(err)
	}
	// 在 venv 中放一个测试文件（没有 marker）
	testFile := filepath.Join(venv, "test.txt")
	if err := os.WriteFile(testFile, []byte("old venv"), 0644); err != nil {
		t.Fatal(err)
	}

	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("python"), 0755); err != nil {
		t.Fatal(err)
	}

	var removedVenv bool
	oldRunner := commandRunner
	commandRunner = func(_ context.Context, name string, args ...string) error {
		if len(args) > 0 && args[0] == "-m" && len(args) > 1 && args[1] == "venv" {
			// venv 创建命令
			venvPath := args[len(args)-1]
			if venvPath == venv {
				removedVenv = true
			}
			venvPython := pythonInVenv(venvPath)
			if err := os.MkdirAll(filepath.Dir(venvPython), 0755); err != nil {
				return err
			}
			return os.WriteFile(venvPython, []byte("venv python"), 0755)
		}
		if len(args) > 2 && args[1] == "pip" && args[2] == "show" {
			return os.ErrNotExist // 触发安装
		}
		return nil
	}
	defer func() { commandRunner = oldRunner }()

	if err := Initialize(context.Background(), structs.PythonConfig{Path: python}, filepath.Join(root, "config.json")); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// 验证旧文件已被删除
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected old venv to be removed, but test file still exists")
	}

	// 验证新 venv 有 marker
	markerPath := filepath.Join(venv, readyMarkerFile)
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("ready marker should exist in new venv: %v", err)
	}

	if !removedVenv {
		t.Error("expected venv to be recreated")
	}
}
