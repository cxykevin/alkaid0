// Package pythonenv manages the global IPython virtual environment.
package pythonenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/log"
)

const (
	venvName        = "venv"
	readyMarkerFile = "alkaid0_inited.txt"
)

var logger = log.New("pythonenv")

var (
	stateMu sync.RWMutex
	venvDir string
	isReady bool
	initErr error

	// initMu 串行化实际初始化，避免多个调用同时重建同一个 venv。
	initMu sync.Mutex

	// asyncMu 防止重复提交异步初始化，同时允许失败后再次重试。
	asyncMu      sync.Mutex
	asyncRunning bool

	// commandRunner is replaceable by package tests so initialization remains offline.
	commandRunner = runCommand

	// commandOutputRunner is replaceable by package tests so version probes remain offline.
	commandOutputRunner = runCommandOutput

	// pythonVersionRunner is replaceable by package tests so version checks remain offline.
	pythonVersionRunner = runPythonVersion
)

const minimumPythonMinor = 12

// minimumDynworkflowVersion dynworkflow 的最低可用版本：workflow 节点终值
// （`node_result`，read @temp/run/<n> 视图里的 `Result:`）自 0.1.3 起才上报；
// 0.1.4 起 Result 可与下游调度同时返回，并修正了 Result 取值。低于该版本的
// 已有 venv 必须升级，否则工作流跑完也读不到任何 Result。
const minimumDynworkflowVersion = "0.1.4"

// requiredPackages 初始化时必须存在的包；minVersion 非空时低于该版本会升级
// （此前的实现只在包缺失时安装，已有旧版的 venv 永远不会更新）。
var requiredPackages = []struct {
	Name       string
	MinVersion string
}{
	{Name: "ipython"},
	{Name: "openai"},
	{Name: "dynworkflow", MinVersion: minimumDynworkflowVersion},
}

// VenvDir returns the most recently initialized virtual environment directory.
// It returns an empty string until Initialize succeeds.
func VenvDir() string {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return venvDir
}

// VenvDirForConfig 返回配置文件对应的虚拟环境目录。
// 与 VenvDir 不同，即使 venv 尚未初始化也能给出预期路径（沙盒需要提前把
// venv 目录设为可读写，而策略在命令执行前就要构建）。
func VenvDirForConfig(configFile string) string {
	if dir := VenvDir(); dir != "" {
		return dir
	}
	if configFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configutil.ExpandPath(configFile)), venvName)
}

// VenvPython returns the Python interpreter path inside the initialized venv.
// It returns an empty string until Initialize succeeds.
func VenvPython() string {
	stateMu.RLock()
	defer stateMu.RUnlock()
	if venvDir == "" {
		return ""
	}
	return pythonInVenv(venvDir)
}

// IsReady returns whether the venv is fully initialized and ready for use.
func IsReady() bool {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return isReady
}

// InitError returns the initialization error if any.
func InitError() error {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return initErr
}

// TestCommand creates an exec.Cmd for testing purposes.
// It's a thin wrapper around exec.CommandContext to avoid exposing
// os/exec in the public API while allowing tests to verify command execution.
func TestCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// InitializeAsync starts venv initialization in a background goroutine.
// It does not block the caller. Use IsReady() to check completion status.
// If the venv directory exists but lacks the ready marker file, it will be
// removed and recreated.
func InitializeAsync(cfg structs.PythonConfig, configPath string) {
	asyncMu.Lock()
	if asyncRunning {
		asyncMu.Unlock()
		return
	}
	asyncRunning = true
	asyncMu.Unlock()

	go func() {
		defer func() {
			asyncMu.Lock()
			asyncRunning = false
			asyncMu.Unlock()
		}()
		ctx := context.Background()
		err := Initialize(ctx, cfg, configPath)
		if err != nil {
			logger.Error("python venv initialization failed: %v", err)
			return
		}
		logger.Info("python venv initialized successfully at: %s", VenvDir())
	}()
}

// Initialize creates or reuses the global IPython virtual environment synchronously.
// This is kept for backward compatibility and testing. Production code should use InitializeAsync.
// configPath is the configuration file path, not its directory. It is accepted
// explicitly to keep this package independent from the config package.
func Initialize(ctx context.Context, cfg structs.PythonConfig, configPath string) error {
	initMu.Lock()
	defer initMu.Unlock()

	stateMu.Lock()
	venvDir = ""
	isReady = false
	initErr = nil
	stateMu.Unlock()

	err := initialize(ctx, cfg, configPath)
	stateMu.Lock()
	defer stateMu.Unlock()
	if err != nil {
		initErr = err
		return err
	}
	path := filepath.Join(filepath.Dir(configutil.ExpandPath(configPath)), venvName)
	venvDir = path
	isReady = true
	return nil
}

func initialize(ctx context.Context, cfg structs.PythonConfig, configPath string) error {
	if ctx == nil {
		return errors.New("pythonenv: nil context")
	}
	if strings.TrimSpace(configPath) == "" {
		return errors.New("pythonenv: empty config path")
	}

	configFile := configutil.ExpandPath(configPath)
	dir := filepath.Dir(configFile)
	path := filepath.Join(dir, venvName)

	// Validate the interpreter before any venv removal or creation.
	python, err := findPython(ctx, cfg.Path)
	if err != nil {
		return err
	}

	// Check if venv exists
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("pythonenv: venv path is not a directory: %s", path)
	}

	// If venv exists but ready marker is missing, remove and recreate
	if err == nil {
		markerPath := filepath.Join(path, readyMarkerFile)
		if _, err := os.Stat(markerPath); os.IsNotExist(err) {
			logger.Warn("venv exists but ready marker missing, removing and recreating: %s", path)
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("pythonenv: failed to remove incomplete venv: %w", err)
			}
			info = nil // Mark as non-existent for recreation
		}
	}

	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("pythonenv: inspect venv: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("pythonenv: create config directory: %w", err)
	}

	venvPython := pythonInVenv(path)
	if info == nil {
		logger.Info("creating python venv at: %s", path)
		if err := commandRunner(ctx, python, "-m", "venv", path); err != nil {
			return fmt.Errorf("pythonenv: create venv: %w", err)
		}
	} else if err := validateExecutable(venvPython); err != nil {
		return fmt.Errorf("pythonenv: invalid venv directory %s: %w", path, err)
	}
	if err := validatePythonVersion(ctx, venvPython); err != nil {
		return fmt.Errorf("pythonenv: invalid venv Python: %w", err)
	}

	for _, pkg := range requiredPackages {
		if err := ensurePackage(ctx, venvPython, cfg.Source, pkg.Name, pkg.MinVersion); err != nil {
			return err
		}
	}

	// Write ready marker
	markerPath := filepath.Join(path, readyMarkerFile)
	if err := os.WriteFile(markerPath, []byte("initialized\n"), 0644); err != nil {
		return fmt.Errorf("pythonenv: write ready marker: %w", err)
	}

	stateMu.Lock()
	venvDir = path
	stateMu.Unlock()

	return nil
}

// ensurePackage 确保包已安装且版本不低于 minVersion（minVersion 为空时只要求已安装）。
// 已安装但过旧时执行 `pip install -U`，并在升级后复查——否则镜像源落后时
// dynworkflow 会静默停留在不支持的版本，workflow 的 Result 永远读不到。
func ensurePackage(ctx context.Context, python, indexURL, name, minVersion string) error {
	installedVersion, installed := installedPackageVersion(ctx, python, name)
	if installed && (minVersion == "" || versionAtLeast(installedVersion, minVersion)) {
		return nil
	}
	if installed {
		logger.Info("%s %s is older than required %s, upgrading...", name, installedVersion, minVersion)
	} else {
		logger.Info("installing %s package...", name)
	}
	args := []string{"-m", "pip", "install"}
	if installed {
		args = append(args, "-U")
	}
	args = append(args, name)
	if strings.TrimSpace(indexURL) != "" {
		args = append(args, "--index-url", indexURL)
	}
	if err := commandRunner(ctx, python, args...); err != nil {
		if installed {
			// 已有旧版可用时不让升级失败连带禁用整个 Python 环境：
			// 旧版 dynworkflow 仍能执行工作流，只是读不到 Result。
			logger.Warn("upgrade %s failed (workflow results may be unavailable): %v", name, err)
			return nil
		}
		return fmt.Errorf("pythonenv: install %s: %w", name, err)
	}
	if minVersion == "" {
		return nil
	}
	version, ok := installedPackageVersion(ctx, python, name)
	if !ok || !versionAtLeast(version, minVersion) {
		logger.Warn("%s %s or newer is required, but %q is installed (workflow results may be unavailable)", name, minVersion, version)
	}
	return nil
}

// installedPackageVersion 读取 venv 中已安装包的版本（pip show）。
// ok=false 表示未安装或读取失败，调用方按未安装处理。
func installedPackageVersion(ctx context.Context, python, name string) (string, bool) {
	output, err := commandOutputRunner(ctx, python, "-m", "pip", "show", name)
	if err != nil {
		return "", false
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "Version:"); found {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// versionAtLeast 比较点分版本号（只比较前三段数字，忽略 dev/rc/post 等后缀）。
func versionAtLeast(got, want string) bool {
	gotParts, wantParts := parseVersionParts(got), parseVersionParts(want)
	for i := range len(wantParts) {
		if gotParts[i] != wantParts[i] {
			return gotParts[i] > wantParts[i]
		}
	}
	return true
}

// parseVersionParts 取版本号前三段数字；缺失或非数字段按 0 处理。
func parseVersionParts(version string) [3]int {
	var parts [3]int
	for i, segment := range strings.SplitN(strings.TrimSpace(version), ".", len(parts)) {
		for _, ch := range segment {
			if ch < '0' || ch > '9' {
				break
			}
			parts[i] = parts[i]*10 + int(ch-'0')
		}
	}
	return parts
}

func findPython(ctx context.Context, configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		// Keep explicit existing paths intact so Windows tests can use a stub executable.
		path := configured
		if !filepath.IsAbs(path) && !strings.ContainsAny(path, `/\\`) {
			var err error
			path, err = exec.LookPath(path)
			if err != nil {
				return "", fmt.Errorf("pythonenv: configured Python is not executable: %w", err)
			}
		} else if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("pythonenv: configured Python is not executable: %w", err)
		}
		if err := validateExecutable(path); err != nil {
			return "", fmt.Errorf("pythonenv: configured Python is not executable: %w", err)
		}
		if err := validatePythonVersion(ctx, path); err != nil {
			return "", fmt.Errorf("pythonenv: configured Python is not supported: %w", err)
		}
		return path, nil
	}
	var versionErr error
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			if validateExecutable(path) != nil {
				continue
			}
			if err := validatePythonVersion(ctx, path); err == nil {
				return path, nil
			} else {
				versionErr = err
			}
		}
	}
	if versionErr != nil {
		return "", fmt.Errorf("pythonenv: no supported Python found: %w", versionErr)
	}
	return "", errors.New("pythonenv: neither python3 nor python is executable")
}

func validatePythonVersion(ctx context.Context, path string) error {
	output, err := pythonVersionRunner(ctx, path)
	if err != nil {
		return fmt.Errorf("pythonenv: failed to determine Python version for %s: %w", path, err)
	}
	version := pythonVersionRegexp.FindStringSubmatch(string(output))
	if len(version) != 3 {
		return fmt.Errorf("pythonenv: failed to parse Python version for %s from %q", path, strings.TrimSpace(string(output)))
	}
	major, err := strconv.Atoi(version[1])
	if err != nil {
		return fmt.Errorf("pythonenv: invalid Python major version %q: %w", version[1], err)
	}
	minor, err := strconv.Atoi(version[2])
	if err != nil {
		return fmt.Errorf("pythonenv: invalid Python minor version %q: %w", version[2], err)
	}
	if major < 3 || (major == 3 && minor < minimumPythonMinor) {
		return fmt.Errorf("pythonenv: Python %d.%d is unsupported; Python 3.12 or newer is required", major, minor)
	}
	return nil
}

var pythonVersionRegexp = regexp.MustCompile(`(?m)Python\s+([0-9]+)\.([0-9]+)`)

func validateExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("path is a directory")
	}
	if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		return errors.New("path is not executable")
	}
	return nil
}

func pythonInVenv(dir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, "Scripts", "python.exe")
	}
	return filepath.Join(dir, "bin", "python")
}

func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// runCommandOutput 执行命令并返回 stdout（用于读取 pip show 的版本信息）。
func runCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

func runPythonVersion(ctx context.Context, name string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, "--version")
	return cmd.CombinedOutput()
}
