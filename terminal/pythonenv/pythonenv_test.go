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

func TestInitializeRejectsInvalidExistingVenv(t *testing.T) {
	root := t.TempDir()
	venv := filepath.Join(root, "venv")
	if err := os.MkdirAll(venv, 0755); err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("python"), 0755); err != nil {
		t.Fatal(err)
	}
	oldRunner := commandRunner
	commandRunner = func(context.Context, string, ...string) error { t.Fatal("must not run commands"); return nil }
	defer func() { commandRunner = oldRunner }()

	if err := Initialize(context.Background(), structs.PythonConfig{Path: python}, filepath.Join(root, "config.json")); err == nil {
		t.Fatal("Initialize succeeded for invalid existing venv")
	}
}
