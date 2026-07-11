package startup

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	_ "embed" // embed logo

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/helper"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/product"
	"github.com/cxykevin/alkaid0/server"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc/connect"
	"github.com/cxykevin/alkaid0/tools/index"
)

const alkaid0IgnoreEntry = "\n# alkaid0\n.alkaid0/\n.alk_*\n"

var logger = log.New("startup")

//go:embed logo.ansi
var logoString string

var versionTemplate = fmt.Sprintf(`
Version:
    Version:      %s (Number %d)
    Commit ID:    %s
Build:
    Time:         %d
    Note:         %s
System:
    OS:           %s
    Arch:         %s
    Current Time: %d
Network:
    User Agent:   %s

%s// if(alkaid0.works){ do_not_panic(); }%s
`,
	product.Version,
	product.VersionID,
	product.CommitID,
	product.BuildTime,
	product.BuildNote,
	runtime.GOOS,
	runtime.GOARCH,
	time.Now().Unix(),
	product.UserAgent,
	"\033[2m\033[3m",
	"\033[0m",
)

var helpTemplate = fmt.Sprintf(`
Usage:
    alkaid0 [command]
Commands:
    help      Show this help message and exit
    version   Show version information and exit
    acp       Start the alkaid0 helper
              (Use alkaid0 acp --help for more information)
    [empty]   Start the server
Environment Variables:
    ALKAID0_DEBUG        Enable debug mode (true | false)
    ALKAID0_LOG_LEVEL    Set log level (default: info)
                         (debug | info | warn | error)
    ALKAID0_LOG_PATH     Set log file path
    ALKAID0_CONFIG_PATH  Set config file path

%s// if(alkaid0.works){ do_not_panic(); }%s
`,
	"\033[2m\033[3m",
	"\033[0m",
)

// Startup 启动程序
func Startup() {
	if len(os.Args) >= 2 && os.Args[1] == "acp" {
		helper.StartHelper(os.Args[1:])
	}

	fmt.Fprintln(os.Stderr, logoString)

	if len(os.Args) >= 2 && (os.Args[1] == "version" || os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(versionTemplate)
		os.Exit(0)
		return
	}

	if len(os.Args) >= 2 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" || os.Args[1] == "/?" || os.Args[1] == "/help" || os.Args[1] == "/H" || os.Args[1] == "/HELP") {
		fmt.Println(helpTemplate)
		os.Exit(0)
		return
	}

	logger.Info("starting alkaid0...")

	// 设置 Go 运行时内存软限制，让 GC 在内存超限时更积极回收并归还给 OS
	// 避免 idle 时 Go 运行时持有过多不释放的内存
	debug.SetMemoryLimit(256 * 1024 * 1024) // 256MB

	openai.Start()
	config.Load()
	log.Load()
	if os.Getenv("ALKAID0_DEBUG") != "true" {
		defer log.SolvePanic()
	}
	ensureGlobalGitIgnore()
	index.Load()

	// 设置信号处理：SIGTERM/SIGINT/SIGQUIT 触发优雅关闭
	// 30 秒超时后强制退出
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	defer stop()

	go func() {
		<-ctx.Done()
		logger.Info("received shutdown signal, initiating graceful shutdown...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := connect.ShutdownWs(shutdownCtx); err != nil {
			logger.Warn("ws server shutdown: %v", err)
		}
		log.Shutdown()
		os.Exit(0)
	}()

	// 读取环境变量 ALKAID0_WORKDIR
	if workdir := os.Getenv("ALKAID0_WORKDIR"); workdir != "" {
		logger.Info("changing workdir to: %s", workdir)
		// 设置工作目录
		_ = os.Chdir(workdir)
	}

	logger.Info("Start server...")
	server.Start()
}
