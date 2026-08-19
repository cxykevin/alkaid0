// Package pythonenv manages the global IPython virtual environment.
package pythonenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
)

// VenvDir returns the most recently initialized virtual environment directory.
// It returns an empty string until Initialize succeeds.
func VenvDir() string {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return venvDir
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

	python, err := findPython(cfg.Path)
	if err != nil {
		return err
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

	for _, packageName := range []string{"ipython", "openai"} {
		if err := commandRunner(ctx, venvPython, "-m", "pip", "show", packageName); err != nil {
			logger.Info("installing %s package...", packageName)
			args := []string{"-m", "pip", "install", packageName}
			if strings.TrimSpace(cfg.Source) != "" {
				args = append(args, "--index-url", cfg.Source)
			}
			if err := commandRunner(ctx, venvPython, args...); err != nil {
				return fmt.Errorf("pythonenv: install %s: %w", packageName, err)
			}
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

func findPython(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("pythonenv: configured Python is not executable: %w", err)
		}
		if err := validateExecutable(path); err != nil {
			return "", fmt.Errorf("pythonenv: configured Python is not executable: %w", err)
		}
		return path, nil
	}
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			if validateExecutable(path) == nil {
				return path, nil
			}
		}
	}
	return "", errors.New("pythonenv: neither python3 nor python is executable")
}

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
