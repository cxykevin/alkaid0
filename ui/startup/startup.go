package startup

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/demo/loop"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/tools/index"
)

// Startup 启动程序
func Startup() {
	openai.Start()
	config.Load()
	log.Load()
	defer log.SolvePanic()
	index.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	// 读取环境变量 ALKAID0_WORKDIR
	if workdir := os.Getenv("ALKAID0_WORKDIR"); workdir != "" {
		// 设置工作目录
		_ = os.Chdir(workdir)
	}
	db := storage.InitStorage("", "")
	defer log.Shutdown()

	// 启动 Demo Loop
	loop.Start(ctx, db)
}
