package storage

import (
	_ "embed" // embed
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/log"
	"gorm.io/gorm"
)

const projectDataPath = ".alkaid0"
const sqliteFileName = "db.sqlite"

var logger *log.LogsObj

func init() {
	logger = log.New("storage")
}

//go:embed auto_execute.sql
var maintenanceSQL string

// maintenanceDone 记录本进程内已经执行过维护语句（VACUUM/ANALYZE）的数据库文件绝对路径。
// 这两个语句都会扫描/重写整个数据库，而 InitStorage 会被反复调用，不能每次打开都执行。
var (
	maintenanceMu   sync.Mutex
	maintenanceDone = make(map[string]struct{})
)

// InitStorage 初始化 db
func InitStorage(dataPath string, dbFile string) (*gorm.DB, error) {
	if dataPath == "" {
		// 读取环境变量：ALKAID_DEBUG_PROJECTPATH 和 ALKAID0_DEBUG_SQLITEFILE
		dataPath = projectDataPath
		if v := os.Getenv("ALKAID_DEBUG_PROJECTPATH"); v != "" {
			dataPath = v
		}
	}

	if dbFile == "" {
		dbFile = sqliteFileName
		if v := os.Getenv("ALKAID_DEBUG_SQLITEFILE"); v != "" {
			dbFile = v
		}
	}

	logger.Info("storage init in %s/%s", dataPath, dbFile)
	if v := os.Getenv("ALKAID_DEBUG_PROJECTPATH"); v != "" {
		logger.Debug("using ALKAID_DEBUG_PROJECTPATH: %s", v)
	}
	if v := os.Getenv("ALKAID_DEBUG_SQLITEFILE"); v != "" {
		logger.Debug("using ALKAID_DEBUG_SQLITEFILE: %s", v)
	}

	// 确保工作目录存在
	if err := os.MkdirAll(dataPath, 0755); err != nil {
		logger.Error("failed to create project data dir %s: %v", dataPath, err)
		return nil, (fmt.Errorf("failed to create project data dir %s: %v", dataPath, err))
	}

	dbPath := filepath.Join(dataPath, dbFile)
	var db *gorm.DB
	var err error
	if db, err = InitDB(dbPath); err != nil {
		logger.Error("failed to init db %s: %v", dataPath, err)
		return nil, err
	}

	// per-connection 的 PRAGMA（foreign_keys 等）已经下沉到 DSN（见 init.go），
	// 这里的语句只剩 VACUUM/ANALYZE：它们不是连接级设置，ANALYZE 会扫描整库索引写
	// sqlite_stat1、VACUUM 会重写整个数据库文件。InitStorage 会被 server/actions 的
	// loadDB 反复调用（打开会话数据库、后台 workflow 事件等），每次打开都执行会明显
	// 拖慢打开速度，因此按数据库文件在本进程内只维护一次；内存库直接跳过。
	if !isMemoryDBPath(dbPath) {
		runMaintenanceOnce(db, dbPath)
	}
	return db, nil
}

// runMaintenanceOnce 对指定数据库文件执行一次 VACUUM/ANALYZE。
// 同一个文件在本进程内只会真正执行一次（无论 InitStorage 被调用多少次）。
func runMaintenanceOnce(db *gorm.DB, dbPath string) {
	key := dbPath
	if abs, absErr := filepath.Abs(dbPath); absErr == nil {
		key = abs
	}

	maintenanceMu.Lock()
	if _, ok := maintenanceDone[key]; ok {
		maintenanceMu.Unlock()
		return
	}
	maintenanceDone[key] = struct{}{}
	maintenanceMu.Unlock()

	for v := range strings.SplitSeq(maintenanceSQL, ";") {
		vs := strings.TrimSpace(v)
		if vs == "" {
			continue
		}
		logger.Debug("executing maintenance SQL: %s", vs)
		if err := db.Exec(vs).Error; err != nil {
			// 维护失败不影响正常使用，只记录
			logger.Error("failed to execute maintenance SQL %q: %v", vs, err)
		}
	}
}
