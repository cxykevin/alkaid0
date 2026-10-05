package migrate

import (
	"testing"

	"gorm.io/gorm"
)

// ApplyLegacyFixtureForTest 向外部测试包（package migrate_test）暴露 legacy 夹具，
// 避免同一目录下两个测试包重复维护旧版 schema DDL 与示例数据。
func ApplyLegacyFixtureForTest(t *testing.T, db *gorm.DB) {
	t.Helper()
	applyLegacyFixture(t, db)
}
