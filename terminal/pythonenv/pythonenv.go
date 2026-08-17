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
)

const venvName = "venv"

var (
	stateMu sync.RWMutex
	venvDir string

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

// Initialize creates or reuses the global IPython virtual environment.
// configPath is the configuration file path, not its directory. It is accepted
// explicitly to keep this package independent from the config package.
func Initialize(ctx context.Context, cfg structs.PythonConfig, configPath string) error {
	if ctx == nil {
		return errors.New("pythonenv: nil context")
	}
	if strings.TrimSpace(configPath) == "" {
		return errors.New("pythonenv: empty config path")
	}

	stateMu.Lock()
	defer stateMu.Unlock()

	configFile := configutil.ExpandPath(configPath)
	dir := filepath.Dir(configFile)
	path := filepath.Join(dir, venvName)
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("pythonenv: venv path is not a directory: %s", path)
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
		if err := commandRunner(ctx, python, "-m", "venv", path); err != nil {
			return fmt.Errorf("pythonenv: create venv: %w", err)
		}
	} else if err := validateExecutable(venvPython); err != nil {
		return fmt.Errorf("pythonenv: invalid venv directory %s: %w", path, err)
	}

	for _, packageName := range []string{"ipython", "openai"} {
		if err := commandRunner(ctx, venvPython, "-m", "pip", "show", packageName); err != nil {
			args := []string{"-m", "pip", "install", packageName}
			if strings.TrimSpace(cfg.Source) != "" {
				args = append(args, "--index-url", cfg.Source)
			}
			if err := commandRunner(ctx, venvPython, args...); err != nil {
				return fmt.Errorf("pythonenv: install %s: %w", packageName, err)
			}
		}
	}
	venvDir = path
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
