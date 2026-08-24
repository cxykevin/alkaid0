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
	oldVersionRunner := pythonVersionRunner
	pythonVersionRunner = func(context.Context, string) ([]byte, error) { return []byte("Python 3.12.0\n"), nil }
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
	defer func() { commandRunner = oldRunner; pythonVersionRunner = oldVersionRunner }()

	if err := Initialize(context.Background(), structs.PythonConfig{Path: python, Source: "https://mirror.invalid/simple"}, configPath); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(root, "venv")
	if got := VenvDir(); got != wantDir {
		t.Fatalf("VenvDir() = %q, want %q", got, wantDir)
	}

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

func TestInitializeRejectsUnsupportedPython(t *testing.T) {
	root := t.TempDir()
	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("python"), 0755); err != nil {
		t.Fatal(err)
	}
	oldVersionRunner := pythonVersionRunner
	oldRunner := commandRunner
	pythonVersionRunner = func(context.Context, string) ([]byte, error) { return []byte("Python 3.11.9\n"), nil }
	commandRunner = func(context.Context, string, ...string) error { t.Fatal("must not run commands"); return nil }
	defer func() { pythonVersionRunner = oldVersionRunner; commandRunner = oldRunner }()

	err := Initialize(context.Background(), structs.PythonConfig{Path: python}, filepath.Join(root, "config.json"))
	if err == nil || !strings.Contains(err.Error(), "Python 3.12") {
		t.Fatalf("Initialize error = %v, want unsupported-version error", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "venv")); !os.IsNotExist(statErr) {
		t.Fatalf("venv was created for unsupported Python: %v", statErr)
	}
}

func TestInitializeRejectsInvalidExistingVenvWithMarker(t *testing.T) {
	root := t.TempDir()
	venv := filepath.Join(root, "venv")
	if err := os.MkdirAll(venv, 0755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(venv, readyMarkerFile)
	if err := os.WriteFile(markerPath, []byte("initialized\n"), 0644); err != nil {
		t.Fatal(err)
	}

	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("python"), 0755); err != nil {
		t.Fatal(err)
	}
	oldVersionRunner := pythonVersionRunner
	oldRunner := commandRunner
	pythonVersionRunner = func(context.Context, string) ([]byte, error) { return []byte("Python 3.12.0\n"), nil }
	commandRunner = func(context.Context, string, ...string) error { t.Fatal("must not run commands"); return nil }
	defer func() { pythonVersionRunner = oldVersionRunner; commandRunner = oldRunner }()

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
	oldVersionRunner := pythonVersionRunner
	pythonVersionRunner = func(context.Context, string) ([]byte, error) { return []byte("Python 3.12.0\n"), nil }
	commandRunner = func(_ context.Context, name string, args ...string) error {
		if len(args) > 0 && args[0] == "-m" && len(args) > 1 && args[1] == "venv" {
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
			return os.ErrNotExist
		}
		return nil
	}
	defer func() { commandRunner = oldRunner; pythonVersionRunner = oldVersionRunner }()

	if err := Initialize(context.Background(), structs.PythonConfig{Path: python}, filepath.Join(root, "config.json")); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("expected old venv to be removed, but test file still exists")
	}
	markerPath := filepath.Join(venv, readyMarkerFile)
	if _, err := os.Stat(markerPath); err != nil {
		t.Errorf("ready marker should exist in new venv: %v", err)
	}
	if !removedVenv {
		t.Error("expected venv to be recreated")
	}
}
