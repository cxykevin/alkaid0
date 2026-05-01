package helper

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/gorilla/websocket"
)

const (
	envConfigPath = "ALKAID0_CONFIG_PATH"
	envHost       = "ALKAID0_HELPER_HOST"
	envPort       = "ALKAID0_HELPER_PORT"
	envPath       = "ALKAID0_HELPER_PATH"
	envKey        = "ALKAID0_HELPER_KEY"
)

var defaultConfigPath = "~/.config/alkaid0/config.json"

// StartHelper 读取配置并连接 websocket，stdin 内容转发到 websocket，websocket 输出写入 stdout
func StartHelper(args []string) {
	cfg, err := buildHelperConfig(args)
	// fmt.Fprintf(os.Stderr, "config: %#v\n", cfg)

	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse arguments: %v\n", err)
		os.Exit(1)
	}

	urlStr, err := buildWebSocketURL(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid websocket url: %v\n", err)
		os.Exit(1)
	}

	conn, resp, err := websocket.DefaultDialer.Dial(urlStr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "websocket dial failed: %v\n", err)
		os.Exit(1)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	defer conn.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	stdinDone := make(chan error, 1)
	wsDone := make(chan error, 1)

	go func() {
		stdinDone <- copyStdinToWS(conn)
	}()
	go func() {
		wsDone <- copyWSToStdout(conn)
	}()

	var firstErr error

	for stdinDone != nil || wsDone != nil {
		select {
		case err := <-stdinDone:
			stdinDone = nil
			if err != nil && !errors.Is(err, io.EOF) {
				firstErr = err
				conn.Close()
				break
			}
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		case err := <-wsDone:
			wsDone = nil
			if err != nil && !errors.Is(err, io.EOF) {
				firstErr = err
				conn.Close()
				return
			}
		case sig := <-sigCh:
			fmt.Fprintf(os.Stderr, "signal received: %s\n", sig)
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			conn.Close()
			return
		}
	}

	if firstErr != nil {
		fmt.Fprintf(os.Stderr, "transfer failed: %v\n", firstErr)
		os.Exit(1)
	}
}

func buildHelperConfig(args []string) (structs.RPCConfig, error) {
	cfg := structs.RPCConfig{
		Host: "127.0.0.1",
		Port: 7433,
		Path: "/acp",
		Key:  "<empty>",
	}

	configPath := fromEnv(envConfigPath, defaultConfigPath)
	if configPath == "" {
		configPath = defaultConfigPath
	}

	flags := flag.NewFlagSet("helper", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPathFlag := flags.String("config", configPath, "path to config file")
	hostFlag := flags.String("host", cfg.Host, "websocket host")
	portFlag := flags.Uint("port", uint(cfg.Port), "websocket port")
	pathFlag := flags.String("path", cfg.Path, "websocket path")
	keyFlag := flags.String("key", cfg.Key, "websocket key")
	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of alkaid0 helper:\n")
		flags.PrintDefaults()
		os.Exit(0)
	}

	if err := flags.Parse(args[1:]); err != nil {
		return cfg, err
	}

	configPath = *configPathFlag
	if envPath := os.Getenv(envConfigPath); envPath != "" {
		configPath = envPath
	}

	loaded, err := loadConfigFile(configPath)
	if err != nil {
		return cfg, err
	}
	mergeRPCConfig(&cfg, loaded)

	if envHost := os.Getenv(envHost); envHost != "" {
		cfg.Host = envHost
	}
	if envPort := os.Getenv(envPort); envPort != "" {
		port, err := strconv.Atoi(envPort)
		if err != nil {
			return cfg, fmt.Errorf("invalid %s: %w", envPort, err)
		}
		cfg.Port = uint16(port)
	}
	if envPath := os.Getenv(envPath); envPath != "" {
		cfg.Path = envPath
	}
	if envKey := os.Getenv(envKey); envKey != "" {
		cfg.Key = envKey
	}

	// Apply flags if they were provided
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "host":
			cfg.Host = *hostFlag
		case "port":
			cfg.Port = uint16(*portFlag)
		case "path":
			cfg.Path = *pathFlag
		case "key":
			cfg.Key = *keyFlag
		}
	})

	if cfg.Port == 0 {
		return cfg, fmt.Errorf("port must be set")
	}
	if cfg.Host == "" {
		return cfg, fmt.Errorf("host must be set")
	}
	if cfg.Path == "" {
		cfg.Path = "/"
	}

	return cfg, nil
}

func loadConfigFile(path string) (structs.RPCConfig, error) {
	if path == "" {
		return structs.RPCConfig{}, nil
	}

	expanded, err := expandPath(path)
	if err != nil {
		return structs.RPCConfig{}, err
	}

	_, err = os.Stat(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return structs.RPCConfig{}, nil
		}
		return structs.RPCConfig{}, err
	}

	data, err := os.ReadFile(expanded)
	if err != nil {
		return structs.RPCConfig{}, err
	}

	var full structs.Config
	if err := json.Unmarshal(data, &full); err == nil {
		return full.Server, nil
	}

	return structs.RPCConfig{}, fmt.Errorf("invalid config file format")
}

func mergeRPCConfig(dst *structs.RPCConfig, src structs.RPCConfig) {
	if src.Host != "" {
		dst.Host = src.Host
	}
	if src.Port != 0 {
		dst.Port = src.Port
	}
	if src.Path != "" {
		dst.Path = src.Path
	}
	if src.Key != "" {
		dst.Key = src.Key
	}
}

func fromEnv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func expandPath(path string) (string, error) {
	if strings.HasPrefix(path, "~+") {
		return "", fmt.Errorf("unsupported path expansion: %s", path)
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func buildWebSocketURL(cfg structs.RPCConfig) (string, error) {
	if cfg.Host == "" {
		return "", errors.New("host is empty")
	}
	if cfg.Port == 0 {
		return "", errors.New("port is empty")
	}
	if cfg.Path == "" {
		cfg.Path = "/"
	}
	if !strings.HasPrefix(cfg.Path, "/") {
		cfg.Path = "/" + cfg.Path
	}

	hostPort := net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	urlObj := url.URL{
		Scheme: "ws",
		Host:   hostPort,
		Path:   cfg.Path,
	}
	if cfg.Key != "" {
		query := url.Values{}
		query.Set("key", cfg.Key)
		urlObj.RawQuery = query.Encode()
	}
	return urlObj.String(), nil
}

func copyStdinToWS(conn *websocket.Conn) error {
	reader := bufio.NewReader(os.Stdin)
	buf := make([]byte, 65536)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				continue
			}
			if buf[0] == '\r' {
				continue
			}
			if writeErr := conn.WriteMessage(websocket.TextMessage, buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			return err
		}
	}
}

func copyWSToStdout(conn *websocket.Conn) error {
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if len(msg) > 0 {
			if _, writeErr := os.Stdout.Write(msg); writeErr != nil {
				return writeErr
			}
			os.Stdout.WriteString("\n")
		}
	}
}
