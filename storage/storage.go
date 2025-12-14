package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cxykevin/alkaid0/log"
)

const projectDataPath = ".alkaid0"
const sqliteFileName = "db.sqlite"

var logger *log.LogsObj

func init() {
	logger = log.New("storage")
}

// InitStorage 初始化 db
func InitStorage() {
	// 读取环境变量：ALKAID_DEBUG_PROJECTPATH 和 ALKAID0_DEBUG_SQLITEFILE
	dataPath := projectDataPath
	if v := os.Getenv("ALKAID_DEBUG_PROJECTPATH"); v != "" {
		dataPath = v
	}

	dbFile := sqliteFileName
	if v := os.Getenv("ALKAID_DEBUG_SQLITEFILE"); v != "" {
		dbFile = v
	}

	logger.Info("storage init in %s/%s", dataPath, dbFile)

	// 确保工作目录存在
	if err := os.MkdirAll(dataPath, 0755); err != nil {
		logger.Error("failed to create project data dir %s: %v", dataPath, err)
		panic(fmt.Errorf("failed to create project data dir %s: %v", dataPath, err))
	}

	dbPath := filepath.Join(dataPath, dbFile)
	if err := InitDB(dbPath); err != nil {
		logger.Error("failed to init db %s: %v", dataPath, err)
		panic(err)
	}
}
