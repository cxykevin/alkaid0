package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var lessMemModeDBID int32

// InitDB 初始化 SQLite 数据库并自动迁移所有表结构。
// 支持内存数据库模式（dbPath 以 :memory: 结尾）。
// 当 ALKAID0_TEST_LESS_MEMORY_MODE 环境变量设置时，
// 内存模式降级为临时文件模式以节省 RAM（用于资源受限的测试环境）。
func InitDB(dbPath string) (*gorm.DB, error) {
	if logger == nil {
		logger = log.New("storage")
	}
	if dbPath == "" {
		dbPath = ".alkaid0/db.sqlite"
	}
	logger.Info("initializing database at: %s", dbPath)

	// 支持内存数据库，当以 :memory: 结尾时将使用 SQLite 内存模式
	if strings.HasSuffix(dbPath, ":memory:") {
		if os.Getenv("ALKAID0_TEST_LESS_MEMORY_MODE") != "" {
			// 降级为临时文件模式，每个连接使用独立的临时数据库文件
			dbPath = strings.ReplaceAll(dbPath, ":memory:", fmt.Sprintf("__lessmem_%d.db", atomic.AddInt32(&lessMemModeDBID, 1)))
		} else {
			dir := filepath.Dir(dbPath)
			if dir != "." {
				// 创建父目录（如果不存在）
				if err := os.MkdirAll(dir, 0755); err != nil {
					return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
				}
			}
		}
	}

	// 使用 gorm 打开连接，注意不要短变量声明遮盖包级的 DB 变量
	var err error
	dialect := sqlite.Open(dbPath)
	db, err := gorm.Open(dialect, &gorm.Config{Logger: New()})
	if err != nil {
		return nil, fmt.Errorf("failed to open db %s: %w", dbPath, err)
	}

	if err := db.AutoMigrate(structs.Tables...); err != nil {
		return nil, fmt.Errorf("failed to automigrate: %w", err)
	}
	logger.Info("database automigrate completed")

	// 初始化全局配置
	db.FirstOrCreate(&structs.Configs{})
	return db, nil
}
