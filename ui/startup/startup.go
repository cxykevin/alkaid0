package startup

import (
	"os"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/helper"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/server"
	"github.com/cxykevin/alkaid0/tools/index"
)

const alkaid0IgnoreEntry = "\n# alkaid0\n.alkaid0/\n.alk_*\n"

var logger = log.New("startup")

// Startup 启动程序
func Startup() {
	if len(os.Args) >= 2 && os.Args[1] == "acp" {
		helper.StartHelper(os.Args[1:])
	}

	logger.Info("starting alkaid0...")
	openai.Start()
	config.Load()
	log.Load()
	if os.Getenv("ALKAID0_DEBUG") != "true" {
		defer log.SolvePanic()
	}
	ensureGlobalGitIgnore()
	index.Load()

	// ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	// defer stop()

	// 读取环境变量 ALKAID0_WORKDIR
	if workdir := os.Getenv("ALKAID0_WORKDIR"); workdir != "" {
		logger.Info("changing workdir to: %s", workdir)
		// 设置工作目录
		_ = os.Chdir(workdir)
	}

	logger.Info("Start server...")
	server.Start()
}
