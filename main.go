package main

import (
	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
)

func main() {
	config.Load()
	log.Load()
}
