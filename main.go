package main

import (
	"os"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
)

func main() {
	config.Load()
	log.Load()
	// 读取环境变量 ALKAID0_WORKDIR
	if workdir := os.Getenv("ALKAID0_WORKDIR"); workdir != "" {
		// 设置工作目录
		os.Chdir(workdir)
	}
}
