package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// DB 数据库连接
var DB *gorm.DB

// InitDB 初始化数据库，返回 error 便于调用方处理
func InitDB(dbPath string) error {
	if dbPath == "" {
		dbPath = ".alkaid0/db.sqlite"
	}

	// 支持内存数据库
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if dir != "." {
			// 创建父目录（如果不存在）
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create db directory %s: %w", dir, err)
			}
		}
	}

	// 使用 gorm 打开连接，注意不要短变量声明遮盖包级的 DB 变量
	var err error
	dialect := sqlite.Open(dbPath)
	DB, err = gorm.Open(dialect, &gorm.Config{Logger: New()})
	if err != nil {
		return fmt.Errorf("failed to open db %s: %w", dbPath, err)
	}

	if err := DB.AutoMigrate(structs.Tables...); err != nil {
		return fmt.Errorf("failed to automigrate: %w", err)
	}

	// 初始化全局配置
	DB.FirstOrCreate(&structs.Configs{})
	return nil
}
