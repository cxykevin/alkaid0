package storage

import (
	"os"
	"path/filepath"
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
