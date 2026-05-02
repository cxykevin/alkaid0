package storage

import (
	"os"
	"testing"

	u "github.com/cxykevin/alkaid0/utils"
)

func TestInit(t *testing.T) {
	// 使用内存数据库进行测试
	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")
	db, _ := InitStorage("", "")
	defer u.Unwrap(db.DB()).Close()
}
