package build

import "github.com/cxykevin/alkaid0/log"

var logger *log.LogsObj

func init() {
	logger = log.New("request:build")
}
