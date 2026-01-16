package storage

import (
	"os"
	"testing"
)

func TestInit(t *testing.T) {
	// 使用内存数据库进行测试
	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")
	InitStorage("", "")
}

func TestMigrate(t *testing.T) {
	// 使用内存数据库进行测试
	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")
	InitStorage("", "")
	InitStorage("", "")
}
