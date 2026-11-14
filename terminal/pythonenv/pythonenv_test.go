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

// writeStubPython 在 root 下创建一个可执行的 python 占位文件。
func writeStubPython(t *testing.T, root string) string {
	t.Helper()
	python := filepath.Join(root, "python")
	if err := os.WriteFile(python, []byte("python"), 0755); err != nil {
		t.Fatal(err)
	}
	return python
}

// stubPythonVersion 固定 Python 版本探测结果。
func stubPythonVersion(t *testing.T, output string) {
	t.Helper()
	old := pythonVersionRunner
	pythonVersionRunner = func(context.Context, string) ([]byte, error) { return []byte(output), nil }
	t.Cleanup(func() { pythonVersionRunner = old })
}

// stubCommandRunner 替换命令执行器：创建 venv 时生成占位解释器，其余命令记录后成功返回。
func stubCommandRunner(t *testing.T) *[][]string {
	t.Helper()
	calls := new([][]string)
	old := commandRunner
	commandRunner = func(_ context.Context, name string, args ...string) error {
		*calls = append(*calls, append([]string{name}, args...))
		if len(args) >= 2 && args[0] == "-m" && args[1] == "venv" {
			venvPython := pythonInVenv(args[len(args)-1])
			if err := os.MkdirAll(filepath.Dir(venvPython), 0755); err != nil {
				return err
			}
			return os.WriteFile(venvPython, []byte("venv python"), 0755)
		}
		return nil
	}
	t.Cleanup(func() { commandRunner = old })
	return calls
}

// stubPackageVersions 替换 `pip show` 的版本探测：versions 里有的包返回对应版本，
// 其余视为未安装。安装步骤可直接改写该 map 以模拟升级成功。
func stubPackageVersions(t *testing.T, versions map[string]string) {
	t.Helper()
	old := commandOutputRunner
	commandOutputRunner = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) >= 4 && args[1] == "pip" && args[2] == "show" {
			if version, ok := versions[args[3]]; ok {
				return []byte("Name: " + args[3] + "\nVersion: " + version + "\n"), nil
			}
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { commandOutputRunner = old })
}

func TestInitializeCreatesVenvAndInstallsPackagesOffline(t *testing.T) {
	root := t.TempDir()
	python := writeStubPython(t, root)
	configPath := filepath.Join(root, "config.json")
	stubPythonVersion(t, "Python 3.12.0\n")
	versions := map[string]string{}
	stubPackageVersions(t, versions)
	var calls [][]string
	oldRunner := commandRunner
	commandRunner = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		if len(args) >= 2 && args[0] == "-m" && args[1] == "venv" {
			venvPython := pythonInVenv(args[len(args)-1])
			if err := os.MkdirAll(filepath.Dir(venvPython), 0755); err != nil {
				return err
			}
			return os.WriteFile(venvPython, []byte("venv python"), 0755)
		}
		// 模拟 pip 安装成功：安装完成后的版本探测返回满足要求的版本。
		if len(args) >= 4 && args[1] == "pip" && args[2] == "install" {
			versions[args[3]] = "0.1.4"
		}
		return nil
	}
	t.Cleanup(func() { commandRunner = oldRunner })

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

	if len(calls) != 4 {
		t.Fatalf("got %d commands, want 4: %#v", len(calls), calls)
	}
	if !reflect.DeepEqual(calls[0][1:], []string{"-m", "venv", wantDir}) {
		t.Errorf("venv command = %#v", calls[0])
	}
	for i, name := range []string{"ipython", "openai", "dynworkflow"} {
		want := []string{"-m", "pip", "install", name, "--index-url", "https://mirror.invalid/simple"}
		if got := calls[i+1][1:]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s install command = %#v, want %#v", name, got, want)
		}
	}
}

func TestInitializeUpgradesOutdatedDynworkflow(t *testing.T) {
	root := t.TempDir()
	python := writeStubPython(t, root)
	stubPythonVersion(t, "Python 3.12.0\n")
	versions := map[string]string{"ipython": "9.0.0", "openai": "2.0.0", "dynworkflow": "0.1.2"}
	stubPackageVersions(t, versions)

	var installs [][]string
	oldRunner := commandRunner
	commandRunner = func(_ context.Context, name string, args ...string) error {
		if len(args) >= 2 && args[0] == "-m" && args[1] == "venv" {
			venvPython := pythonInVenv(args[len(args)-1])
			if err := os.MkdirAll(filepath.Dir(venvPython), 0755); err != nil {
				return err
			}
			return os.WriteFile(venvPython, []byte("venv python"), 0755)
		}
		installs = append(installs, append([]string{name}, args...))
		// 模拟 pip 升级成功：升级后的版本探测返回满足最低要求的版本。
		versions["dynworkflow"] = "0.1.4"
		return nil
	}
	t.Cleanup(func() { commandRunner = oldRunner })

	if err := Initialize(context.Background(), structs.PythonConfig{Path: python}, filepath.Join(root, "config.json")); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("got %d install commands, want 1: %#v", len(installs), installs)
	}
	if got := installs[0][1:]; !reflect.DeepEqual(got, []string{"-m", "pip", "install", "-U", "dynworkflow"}) {
		t.Errorf("upgrade command = %#v", got)
	}
}

// TestInitializeKeepsOutdatedPackageWhenUpgradeFails 升级失败（离线/镜像源落后）时
// 不让整个 Python 环境不可用：旧版仍可执行工作流，只是暂时读不到 Result。
func TestInitializeKeepsOutdatedPackageWhenUpgradeFails(t *testing.T) {
	root := t.TempDir()
	python := writeStubPython(t, root)
	stubPythonVersion(t, "Python 3.12.0\n")
	stubPackageVersions(t, map[string]string{"ipython": "9.0.0", "openai": "2.0.0", "dynworkflow": "0.1.2"})

	oldRunner := commandRunner
	commandRunner = func(_ context.Context, name string, args ...string) error {
		if len(args) >= 2 && args[0] == "-m" && args[1] == "venv" {
			venvPython := pythonInVenv(args[len(args)-1])
			if err := os.MkdirAll(filepath.Dir(venvPython), 0755); err != nil {
				return err
			}
			return os.WriteFile(venvPython, []byte("venv python"), 0755)
		}
		return os.ErrPermission
	}
	t.Cleanup(func() { commandRunner = oldRunner })

	if err := Initialize(context.Background(), structs.PythonConfig{Path: python}, filepath.Join(root, "config.json")); err != nil {
		t.Fatalf("Initialize should tolerate a failed upgrade of an installed package: %v", err)
	}
	if !IsReady() {
		t.Fatal("venv should still be usable with the outdated dynworkflow")
	}
}

func TestInitializeSkipsUpToDatePackages(t *testing.T) {
	root := t.TempDir()
	python := writeStubPython(t, root)
	stubPythonVersion(t, "Python 3.12.0\n")
	stubPackageVersions(t, map[string]string{"ipython": "9.0.0", "openai": "2.0.0", "dynworkflow": "0.1.4"})
	calls := stubCommandRunner(t)

	if err := Initialize(context.Background(), structs.PythonConfig{Path: python}, filepath.Join(root, "config.json")); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("got %d commands, want only venv creation: %#v", len(*calls), *calls)
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		got, want string
		ok        bool
	}{
		{"0.1.4", "0.1.4", true},
		{"0.1.3", "0.1.4", false},
		{"0.1.10", "0.1.4", true},
		{"0.2", "0.1.4", true},
		{"1.0.0", "0.1.4", true},
		{"0.1.4rc1", "0.1.4", true},
		{"0.1", "0.1.4", false},
		{"", "0.1.4", false},
	}
	for _, tt := range cases {
		if got := versionAtLeast(tt.got, tt.want); got != tt.ok {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", tt.got, tt.want, got, tt.ok)
		}
	}
}

func TestInitializeRejectsUnsupportedPython(t *testing.T) {
	root := t.TempDir()
	python := writeStubPython(t, root)
	stubPythonVersion(t, "Python 3.11.9\n")
	oldRunner := commandRunner
	commandRunner = func(context.Context, string, ...string) error { t.Fatal("must not run commands"); return nil }
	t.Cleanup(func() { commandRunner = oldRunner })

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

	python := writeStubPython(t, root)
	stubPythonVersion(t, "Python 3.12.0\n")
	oldRunner := commandRunner
	commandRunner = func(context.Context, string, ...string) error { t.Fatal("must not run commands"); return nil }
	t.Cleanup(func() { commandRunner = oldRunner })

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

	python := writeStubPython(t, root)
	stubPythonVersion(t, "Python 3.12.0\n")
	stubPackageVersions(t, map[string]string{"ipython": "9.0.0", "openai": "2.0.0", "dynworkflow": "0.1.4"})

	var removedVenv bool
	oldRunner := commandRunner
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
		return nil
	}
	t.Cleanup(func() { commandRunner = oldRunner })

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
