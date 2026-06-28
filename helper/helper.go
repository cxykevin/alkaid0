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
	"runtime"
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

// systemConfigPathFn 返回平台相关的系统级配置文件路径，默认为函数，测试时可替换
var systemConfigPathFn = func() string {
	if runtime.GOOS == "windows" {
		return `C:\ProgramData\alkaid0\config.json`
	}
	return "/etc/alkaid0/config.json"
}

// StartHelper 读取配置并连接 websocket，实现 stdin与websocket 的双向转发。
// 使用两个独立 goroutine 处理双向通信，分别追踪完成状态：
//   - stdinDone: stdin 读取完毕(EOF)或错误时触发，随后发送正常关闭信号
//   - wsDone: WebSocket 关闭或错误时触发
//
// 两个通道都关闭才能安全退出 select 循环，防止单方面关闭导致的数据丢失
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

	// 信号处理：优雅关闭连接
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// 两个通道分别跟踪 stdin 转发和 ws 接收的完成状态
	stdinDone := make(chan error, 1)
	wsDone := make(chan error, 1)

	// goroutine 1: 将 stdin 内容逐行转发到 WebSocket
	go func() {
		stdinDone <- copyStdinToWS(conn)
	}()
	// goroutine 2: 将 WebSocket 收到的消息写入 stdout
	go func() {
		wsDone <- copyWSToStdout(conn)
	}()

	var firstErr error

	// 三路 select：stdin 完成 / WebSocket 关闭 / 系统信号
	for stdinDone != nil || wsDone != nil {
		select {
		case err := <-stdinDone:
			stdinDone = nil
			if err != nil && !errors.Is(err, io.EOF) {
				firstErr = err
				conn.Close()
				break
			}
			// stdin 正常结束，发送 WebSocket 关闭帧
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

// buildHelperConfig 根据 args 构建 RPC 配置，配置来源优先级（低→高）：
//  1. 代码硬编码默认值 (Host=127.0.0.1, Port=7433)
//  2. 配置文件（系统级 /etc/alkaid0/config.json → 用户级 ~/.config/alkaid0/config.json，后覆盖前）
//  3. 环境变量 (ALKAID0_HELPER_HOST/PORT/PATH/KEY)
//  4. 命令行 flag（最高优先级，flags.Visit() 确保只覆盖显式指定的 flag）
func buildHelperConfig(args []string) (structs.RPCConfig, error) {
	// 第 0 层：代码硬编码默认值
	cfg := structs.RPCConfig{
		Host: "127.0.0.1",
		Port: 7433,
		Path: "/acp",
		Key:  "<empty>",
	}

	// 第 1 步：解析 -config flag（用于读取配置文件）
	// 先用默认值初始化 flags，这样在未提供 -config 时也能正确 fallback
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

	// 第 2 步：从配置文件加载（若存在且可解析）
	// 先取 -config flag 的值，如果环境变量 ALKAID0_CONFIG_PATH 存在则覆盖
	configPath = *configPathFlag

	configExplicit := false
	if envPath := os.Getenv(envConfigPath); envPath != "" {
		configPath = envPath
		configExplicit = true
	}
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicit = true
		}
	})

	// 加载配置文件链：非显式指定时先加载系统级配置；显式指定时仅加载指定配置
	loadConfigChain(&cfg, configPath, configExplicit)

	// 第 3 步：环境变量覆盖（优先级高于配置文件）
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

	// 第 4 步：命令行 flag 最高优先级覆盖
	// 使用 flags.Visit() 而非 flags.Lookup()，确保仅覆盖用户显式指定的 flag
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

	// 校验必要参数
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

	// loadConfigChain 按优先级顺序加载多个配置文件，后加载的覆盖先加载的同名字段。
// 当未显式指定配置路径时，先尝试加载系统级配置，再加载用户配置进行覆盖。
// 当显式指定配置路径时，仅加载指定配置。
func loadConfigChain(cfg *structs.RPCConfig, userConfigPath string, configExplicit bool) {
	if !configExplicit {
		// 未显式指定时，先加载系统级配置作为基座
		if sysPath := systemConfigPathFn(); sysPath != "" {
			if loaded, err := loadConfigFile(sysPath); err == nil {
				mergeRPCConfig(cfg, loaded)
			}
		}
	}

	// 加载用户配置（或显式指定的配置），覆盖低优先级的值
	if loaded, err := loadConfigFile(userConfigPath); err == nil {
		mergeRPCConfig(cfg, loaded)
	}
}

// loadConfigFile 从指定路径加载配置文件并提取 RPC 配置
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

// mergeRPCConfig 将 src 中的非空字段合并到 dst 中
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

// fromEnv 读取环境变量，未设置时返回默认值

func fromEnv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// expandPath 展开路径中的 ~/ 为家目录

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

// buildWebSocketURL 根据 RPC 配置构建 WebSocket URL，支持在查询参数中携带认证密钥
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

// copyStdinToWS 将标准输入内容逐块转发到 WebSocket 连接。
// 使用 64KB 缓冲区读取 stdin，跳过纯换行输入，连接断开或 EOF 时返回。
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

// copyWSToStdout 将 WebSocket 接收到的消息逐条写入标准输出（每条后附加换行）。
// 连接关闭或出错时返回。
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
