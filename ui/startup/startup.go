package startup

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/demo/loop"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/tools/index"
)

const alkaid0IgnoreEntry = "\n# alkaid0\n.alkaid0/\n.alk_*\n"

var logger = log.New("startup")

// Startup 启动程序
func Startup() {
	logger.Info("starting alkaid0...")
	openai.Start()
	config.Load()
	log.Load()
	defer log.SolvePanic()
	ensureGlobalGitIgnore()
	index.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	// 读取环境变量 ALKAID0_WORKDIR
	if workdir := os.Getenv("ALKAID0_WORKDIR"); workdir != "" {
		logger.Info("changing workdir to: %s", workdir)
		// 设置工作目录
		_ = os.Chdir(workdir)
	}
	logger.Info("initializing storage...")
	db := storage.InitStorage("", "")
	defer log.Shutdown()

	// 启动 Demo Loop
	logger.Info("starting demo loop...")
	loop.Start(ctx, db)
}

func ensureGlobalGitIgnore() {
	markerPath, err := gitInitMarkerPath()
	if err != nil {
		logger.Warn("resolve git init marker path failed: %v", err)
		return
	}
	if markerPath != "" {
		if _, err := os.Stat(markerPath); err == nil {
			return
		}
	}

	gitPath, fromConfig, err := getGitGlobalExcludePath()
	if err != nil {
		logger.Warn("git global excludesfile resolve failed: %v", err)
		return
	}

	if gitPath == "" {
		return
	}

	expanded := configutil.ExpandPath(gitPath)
	if expanded == "" {
		return
	}

	if err := ensureIgnoreFile(expanded); err != nil {
		logger.Warn("ensure gitignore file failed: %v", err)
		return
	}

	if err := appendIgnoreIfMissing(expanded); err != nil {
		logger.Warn("append gitignore entry failed: %v", err)
		return
	}

	if !fromConfig {
		if err := setGitGlobalExcludePath(expanded); err != nil {
			logger.Warn("set git global excludesfile failed: %v", err)
			return
		}
	}

	if markerPath != "" {
		if err := writeGitInitMarker(markerPath); err != nil {
			logger.Warn("write git init marker failed: %v", err)
			return
		}
	}
}

func gitInitMarkerPath() (string, error) {
	configPath := config.ConfigPath()
	if configPath == "" {
		return "", nil
	}
	expanded := configutil.ExpandPath(configPath)
	if expanded == "" {
		return "", nil
	}
	return filepath.Join(filepath.Dir(expanded), "git-inited.txt"), nil
}

func writeGitInitMarker(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	return file.Close()
}

func getGitGlobalExcludePath() (string, bool, error) {
	if value, ok := readGitConfigValue("--global", "core.excludesfile"); ok {
		return value, true, nil
	}

	defaultPath := defaultGitExcludePath()
	if defaultPath != "" {
		expanded := configutil.ExpandPath(defaultPath)
		if expanded != "" {
			if _, err := os.Stat(expanded); err == nil {
				return defaultPath, false, nil
			}
		}
	}

	return "~/.gitignore", false, nil
}

func defaultGitExcludePath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "git", "ignore")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "git", "ignore")
}

func readGitConfigValue(scope string, key string) (string, bool) {
	cmd := exec.Command("git", "config", scope, key)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	val := strings.TrimSpace(string(out))
	if val == "" {
		return "", false
	}
	return val, true
}

func setGitGlobalExcludePath(path string) error {
	cmd := exec.Command("git", "config", "--global", "core.excludesfile", path)
	return cmd.Run()
}

func ensureIgnoreFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	return file.Close()
}

func appendIgnoreIfMissing(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := string(data)
	if strings.Contains(content, "\n.alk_*") || strings.HasSuffix(strings.TrimSpace(content), ".alk_*") {
		return nil
	}

	content = strings.TrimRight(content, "\n") + alkaid0IgnoreEntry
	return os.WriteFile(path, []byte(content), 0644)
}
