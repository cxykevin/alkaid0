package index

import "github.com/cxykevin/alkaid0/log"

var logger *log.LogsObj

func init() {
	logger = log.New("tools:index")
}

// PkgIndexs 加载器
var PkgIndexs []func() string

// AddIndex 添加加载器
func AddIndex(index func() string) {
	PkgIndexs = append(PkgIndexs, index)
}

// Load 加载所有加载器
func Load() {
	for _, index := range PkgIndexs {
		ret := index()
		logger.Info("load tool: \"%s\"", ret)
	}
}
