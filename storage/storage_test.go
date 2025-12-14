package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInit(t *testing.T) {
	// 更改环境变量
	os.Setenv("ALKAID_DEBUG_PROJECTPATH", "../debug_config/dot_alkaid")
	InitStorage()
	// 验证目录存在
	dataPath := os.Getenv("ALKAID_DEBUG_PROJECTPATH")
	if dataPath == "" {
		dataPath = ".alkaid0"
	}
	dbPath := filepath.Join(dataPath, "db.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file not found: %v", err)
	}
	// 清理测试产物
	// os.RemoveAll(dataPath)
}
