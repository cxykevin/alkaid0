package storage

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var lessMemModeDBID int32

// perConnPragmas 每个新建连接都必须执行的 SQLite PRAGMA 设置。
//
// 这些都是 **per-connection** 属性：foreign_keys / cache_size / synchronous /
// mmap_size 都不会持久化到数据库文件，连接池回收（ConnMaxLifetime 到期、连接出错
// 被丢弃等）后新建的连接会退回 SQLite 默认值——其中 foreign_keys 默认 OFF，
// 意味着外键约束会静默失效。此前这些语句在 InitStorage 里用 db.Exec 执行一次，
// 只对当时那一条连接生效，连接回收后全部丢失（实测回收后 foreign_keys 由 1 变 0）。
//
// glebarez/go-sqlite 会在每次 Open 新连接时执行 DSN 里的 _pragma 参数
// （见 go-sqlite/sqlite.go 的 applyQueryParams），因此这里统一写进 DSN，
// 保证连接池中每一条连接都带上这些设置。
var perConnPragmas = []string{
	"journal_mode(WAL)",
	"foreign_keys(ON)",
	"synchronous(NORMAL)",
	"cache_size(-512)",
	"mmap_size(2000000)",
	"page_size(4096)",
}

// isMemoryDBPath 判断 dbPath 是否指向 SQLite 内存数据库。
// InitStorage 用 filepath.Join 拼接 dataPath 与 dbFile，因此内存库的实际形态
// 通常是 ".alkaid0/:memory:" 这种“以 :memory: 结尾”的路径。
func isMemoryDBPath(dbPath string) bool {
	return strings.HasSuffix(dbPath, ":memory:")
}

// buildDSN 在 SQLite DSN 上追加 per-connection PRAGMA（_pragma 参数）。
func buildDSN(dbPath string) string {
	sep := "?"
	if strings.ContainsRune(dbPath, '?') {
		sep = "&"
	}
	q := url.Values{}
	for _, p := range perConnPragmas {
		q.Add("_pragma", p)
	}
	return dbPath + sep + q.Encode()
}

// scopesPKMigrationBackup scopes 主键迁移期间暂存旧表的表名
const scopesPKMigrationBackup = "scopes__pk_migration_old"

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

	// 支持内存数据库，当以 :memory: 结尾时将使用 SQLite 内存模式
	if isMemoryDBPath(dbPath) {
		if os.Getenv("ALKAID0_TEST_LESS_MEMORY_MODE") != "" {
			// 降级为临时文件模式，每个连接使用独立的临时数据库文件
			// 临时 DB 放入系统临时目录，避免测试反复运行在项目数据目录累积 __lessmem_*.db 文件
			// 文件名加入进程号：go test 并行运行多个测试包时各进程的 lessMemModeDBID 均从 0
			// 开始，不加进程号会在 Windows 上产生跨进程同名文件冲突（SQLite 表已存在等偶发错误）
			dbPath = filepath.Join(os.TempDir(), fmt.Sprintf("alkaid0_lessmem_%d_%d.db", os.Getpid(), atomic.AddInt32(&lessMemModeDBID, 1)))
		} else {
			// 必须归一化为 SQLite 真正识别为内存库的名字：只有恰好等于 ":memory:"
			// （或 file::memory: URI）才走内存模式。".alkaid0/:memory:" 这类后缀路径
			// 会被 SQLite 当成普通文件名，实际落盘为名为 ":memory:" 的文件
			// （此前测试目录里就真实出现过 :memory: / :memory:-wal / :memory:-shm）。
			dbPath = ":memory:"
		}
	}
	logger.Info("initializing database at: %s", dbPath)

	// 使用 gorm 打开连接，注意不要短变量声明遮盖包级的 DB 变量
	var err error
	dialect := sqlite.Open(buildDSN(dbPath))
	db, err := gorm.Open(dialect, &gorm.Config{Logger: New()})
	if err != nil {
		return nil, fmt.Errorf("failed to open db %s: %w", dbPath, err)
	}

	// 限制 SQLite 连接池：单连接模式避免锁争用，也减少内存开销
	// SQLite 本质上是单写入者数据库，多个连接只会增加内存和锁冲突
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// 迁移期间显式关闭外键检查：
	// DSN 里已经让每条新连接默认 foreign_keys=ON，而历史数据库可能存在孤儿行
	// （旧版本删除会话时不清理子表），GORM 为已存在的表补建外键约束时会重建表
	// 并整表拷贝，外键开启会让整个 AutoMigrate 直接失败。迁移完成后再恢复。
	// 连接池此时已被限制为单连接，因此这两条 PRAGMA 与 AutoMigrate 走同一条连接。
	if err := db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		return nil, fmt.Errorf("failed to disable foreign keys before automigrate: %w", err)
	}
	migrateErr := db.AutoMigrate(structs.Tables...)
	// 必须在任何 return 之前恢复，否则后续正常读写都会在无外键检查下进行
	reenableErr := db.Exec("PRAGMA foreign_keys = ON").Error
	if reenableErr != nil {
		return nil, fmt.Errorf("failed to re-enable foreign keys after automigrate: %w", reenableErr)
	}
	if migrateErr != nil {
		return nil, fmt.Errorf("failed to automigrate: %w", migrateErr)
	}
	// AutoMigrate 不会修改已有表的主键，历史库的 scopes 表需要单独修复
	if err := migrateScopesPrimaryKey(db); err != nil {
		return nil, fmt.Errorf("failed to migrate scopes primary key: %w", err)
	}
	logger.Info("database automigrate completed")

	// 初始化全局配置（单行记录），并读入内存缓存
	db.FirstOrCreate(&structs.Configs{})
	_ = ReadGlobalConfigs(db)
	return db, nil
}

// migrateScopesPrimaryKey 修复历史数据库中 scopes 表缺少 (chat_id, name) 复合主键的问题。
//
// 背景：Scopes 最初以 name 为单列主键，ChatID 是后来才加入主键的。GORM 的 AutoMigrate
// 永远不会修改已有表的主键（gorm/migrator.MigrateColumn 里所有主键差异都被跳过），
// 因此旧库升级后 scopes.name 仍然是单列唯一键：不同会话无法保存同名 scope，
// SetScopeEnabled 会直接拿到 "UNIQUE constraint failed: scopes.name"。
// SQLite 不支持 ALTER TABLE ... ADD PRIMARY KEY，只能重建表：
// 改名旧表 → AutoMigrate 建出带复合主键（和外键）的新表 → 回填数据 → 删除旧表。
func migrateScopesPrimaryKey(db *gorm.DB) error {
	if !db.Migrator().HasTable(&structs.Scopes{}) {
		return nil
	}
	var cols []struct {
		Name string `gorm:"column:name"`
		PK   int    `gorm:"column:pk"`
	}
	if err := db.Raw("PRAGMA table_info(scopes)").Scan(&cols).Error; err != nil {
		return err
	}
	pkOf := make(map[string]int, len(cols))
	for _, c := range cols {
		pkOf[c.Name] = c.PK
	}
	if pkOf["chat_id"] > 0 && pkOf["name"] > 0 {
		return nil // 已经是 (chat_id, name) 复合主键
	}
	logger.Info("migrating scopes primary key to (chat_id, name)")

	// DDL 在 SQLite 里是事务性的，整体放进事务避免中途失败留下改名到一半的表
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DROP TABLE IF EXISTS " + scopesPKMigrationBackup).Error; err != nil {
			return err
		}
		if err := tx.Exec("ALTER TABLE scopes RENAME TO " + scopesPKMigrationBackup).Error; err != nil {
			return err
		}
		// 新表由模型定义创建，自动带上复合主键与外键
		if err := tx.AutoMigrate(&structs.Scopes{}); err != nil {
			return err
		}
		// 只回填会话仍存在的行：历史孤儿行（会话已删）无法满足外键，保留没有意义
		if err := tx.Exec("INSERT OR REPLACE INTO scopes (chat_id, name, enabled) " +
			"SELECT chat_id, name, enabled FROM " + scopesPKMigrationBackup + " " +
			"WHERE chat_id IN (SELECT id FROM chats)").Error; err != nil {
			return err
		}
		return tx.Exec("DROP TABLE " + scopesPKMigrationBackup).Error
	})
}
