package main

import (
	"os"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/demo/loop"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/tools/index"
)

func main() {
	config.Load()
	log.Load()
	index.Load()
	// 读取环境变量 ALKAID0_WORKDIR
	if workdir := os.Getenv("ALKAID0_WORKDIR"); workdir != "" {
		// 设置工作目录
		os.Chdir(workdir)
	}
	db := storage.InitStorage("", "")

	// 启动 Demo Loop
	loop.Start(db)
}
