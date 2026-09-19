package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestInit(t *testing.T) {
	// 使用内存数据库进行测试
	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")
	db, err := InitStorage("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer u.Unwrap(db.DB()).Close()
}

func TestInitDBMigratesHiddenColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")

	legacy, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if err := legacy.Exec(`CREATE TABLE chats (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT, updated_at DATETIME)`).Error; err != nil {
		t.Fatalf("create legacy chats table: %v", err)
	}
	if err := legacy.Exec(`INSERT INTO chats (title) VALUES (?)`, "legacy").Error; err != nil {
		t.Fatalf("insert legacy chat: %v", err)
	}
	legacySQL, err := legacy.DB()
	if err != nil {
		t.Fatalf("get legacy database handle: %v", err)
	}
	if err := legacySQL.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	defer u.Unwrap(db.DB()).Close()

	var column struct {
		Name    string
		NotNull int `gorm:"column:notnull"`
		Default any `gorm:"column:dflt_value"`
	}
	if err := db.Raw("PRAGMA table_info(chats)").Scan(&column).Error; err != nil {
		t.Fatalf("inspect chats schema: %v", err)
	}
	var hidden struct {
		Name    string `gorm:"column:name"`
		NotNull int    `gorm:"column:notnull"`
		Default string `gorm:"column:dflt_value"`
	}
	var columns []struct {
		Name    string  `gorm:"column:name"`
		NotNull int     `gorm:"column:notnull"`
		Default *string `gorm:"column:dflt_value"`
	}
	if err := db.Raw("PRAGMA table_info(chats)").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect chats columns: %v", err)
	}
	for _, c := range columns {
		if c.Name == "hidden" {
			hidden.Name, hidden.NotNull = c.Name, c.NotNull
			if c.Default != nil {
				hidden.Default = *c.Default
			}
		}
	}
	if hidden.Name != "hidden" || hidden.NotNull != 1 || hidden.Default != "false" {
		t.Fatalf("hidden schema = %+v, want NOT NULL DEFAULT false", hidden)
	}

	var chat structs.Chats
	if err := db.Where("title = ?", "legacy").First(&chat).Error; err != nil {
		t.Fatalf("read migrated legacy chat: %v", err)
	}
	if chat.Hidden {
		t.Error("legacy chat should default to visible")
	}
}

// TestLoggerParamsFilterHidesValues 回归测试：GORM 日志里不得出现参数值。
//
// 背景：GORM 传给 logger 的是 Dialector.Explain 插值后的 SQL，参数值（原始密钥、
// 完整提示词、文件内容）会被原样写进日志；而日志尾部会被 /feedback 上传。
// 本用例走的是 GORM 真实的调用路径（ParamsFilter → Explain）。
func TestLoggerParamsFilterHidesValues(t *testing.T) {
	const secret = "sk-super-secret-value-1234567890"

	logger, ok := New().(*Logger)
	if !ok {
		t.Fatal("New() should return *Logger")
	}

	rawSQL := "INSERT INTO `key_mappings` (`original`,`fake`) VALUES (?,?)"
	filteredSQL, filteredVars := logger.ParamsFilter(context.Background(), rawSQL, secret, "fake-value")

	if len(filteredVars) != 0 {
		t.Fatalf("ParamsFilter must drop all params, got %d", len(filteredVars))
	}

	// 模拟 GORM callbacks.go：用过滤后的 (sql, vars) 做 Explain
	explained := sqlite.Dialector{}.Explain(filteredSQL, filteredVars...)
	if strings.Contains(explained, secret) {
		t.Errorf("interpolated log SQL must not contain the secret: %s", explained)
	}
	if !strings.Contains(explained, "?") {
		t.Errorf("placeholders must survive so the statement stays readable: %s", explained)
	}

	// 对照：不过滤时确实会泄漏（证明该用例能区分两种实现）
	leaked := sqlite.Dialector{}.Explain(rawSQL, secret, "fake-value")
	if !strings.Contains(leaked, secret) {
		t.Skip("dialector does not interpolate in this build; e2e assertion is moot")
	}
}
