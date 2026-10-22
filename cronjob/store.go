package cronjob

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/log"
	"github.com/glebarez/sqlite"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var logger = log.New("cronjob")

// perConnPragmas 每条新连接都要执行的 PRAGMA（写进 DSN，连接池回收后不会丢失）。
// WAL 让调度器读取与工具层写入互不阻塞；busy_timeout 避免瞬时写锁报错。
var perConnPragmas = []string{
	"journal_mode(WAL)",
	"busy_timeout(5000)",
	"synchronous(NORMAL)",
}

// buildDSN 在 SQLite DSN 上追加 per-connection PRAGMA。
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

// Store cron.sqlite 的持久层。
//
// 单连接模式（与主库 storage.InitDB 一致）：SQLite 本质单写入者，多连接只会增加
// 锁冲突；调度器在 1s tick 内只做内存比较，读写都很少。连接在进程生命周期内常驻，
// 进程退出时由操作系统回收（信号处理走 os.Exit，不需要显式 Close）。
type Store struct {
	db   *gorm.DB
	path string

	changed   chan struct{}
	closeOnce sync.Once
}

// Path 返回 cron.sqlite 的绝对/清理后路径。
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Open 打开（必要时创建）configDir/cron.sqlite，迁移表结构并写入 schema 版本。
func Open(configDir string) (*Store, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, fmt.Errorf("cronjob: config dir is empty")
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, fmt.Errorf("cronjob: create config dir %s: %w", configDir, err)
	}
	dbPath := filepath.Join(configDir, DBFileName)
	db, err := gorm.Open(sqlite.Open(buildDSN(dbPath)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("cronjob: open %s: %w", dbPath, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("cronjob: get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := db.AutoMigrate(&Job{}, &metadata{}); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("cronjob: migrate %s: %w", dbPath, err)
	}
	if err := ensureSchemaVersion(db); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("cronjob: ensure schema version: %w", err)
	}
	logger.Info("cronjob store opened at %s", dbPath)
	return &Store{
		db:      db,
		path:    dbPath,
		changed: make(chan struct{}, 1),
	}, nil
}

// ensureSchemaVersion 在 metadata 表中写入 schema_version（已存在则保持不变）。
func ensureSchemaVersion(db *gorm.DB) error {
	var row metadata
	err := db.Where("key = ?", schemaVersionKey).First(&row).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return db.Create(&metadata{
		Key:       schemaVersionKey,
		Value:     schemaVersion,
		UpdatedAt: time.Now(),
	}).Error
}

// SchemaVersion 返回 cron.sqlite 中记录的 schema 版本（测试与诊断用）。
func (s *Store) SchemaVersion() (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("cronjob: store is closed")
	}
	var row metadata
	if err := s.db.Where("key = ?", schemaVersionKey).First(&row).Error; err != nil {
		return "", err
	}
	return row.Value, nil
}

// shorthandCron 简写语法（预定义描述符）到标准 5 段表达式的映射。
//
// 用户 2026-10-22 要求：允许简写，写入时直接翻译成原始 cron 表达式。因此
// 「入库/回显」一律是 5 段表达式，简写只作为输入便利；@every Ns 这类无法用
// 5 段等价表示的写法不支持。
var shorthandCron = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// ExpandCron 把简写语法翻译成标准 5 段表达式（大小写不敏感）。
//
// 非简写写法只去首尾空白、原样返回，交给 cron.ParseStandard 校验；这样调用方
// 可以把 ExpandCron 当作「入库前的统一写法」无条件使用。
func ExpandCron(expr string) string {
	trimmed := strings.TrimSpace(expr)
	if v, ok := shorthandCron[strings.ToLower(trimmed)]; ok {
		return v
	}
	return trimmed
}

// ParseSchedule 解析 cron 表达式（标准 5 段；简写先由 ExpandCron 展开）。
//
// 时间语义为本地时区：Next 结果落在传入时间的 Location 上，调用方通常传 time.Now()。
func ParseSchedule(expr string) (cron.Schedule, error) {
	expanded := ExpandCron(expr)
	if expanded == "" {
		return nil, fmt.Errorf("cronjob: cron expression is empty")
	}
	sched, err := cron.ParseStandard(expanded)
	if err != nil {
		return nil, fmt.Errorf("cronjob: invalid cron expression %q: %w", expr, err)
	}
	return sched, nil
}

// ValidateCron 只校验 cron 表达式（工具层在定稿参数时先自查，避免带着非法表达式走到存储层）。
func ValidateCron(expr string) error {
	_, err := ParseSchedule(expr)
	return err
}

// Validate 校验一条任务是否可以入库/调度。
//
// 表达式采用标准 5 段 cron（同时接受 @hourly 等预定义描述符），由 robfig/cron 解析；
// 时间语义为本地时区（解析结果的 Next 落在传入时间的时区上）。
func Validate(j Job) error {
	if _, err := ParseSchedule(j.Cron); err != nil {
		return err
	}
	if strings.TrimSpace(j.Workdir) == "" {
		return fmt.Errorf("cronjob: workdir is empty")
	}
	if j.SessionID == 0 {
		return fmt.Errorf("cronjob: session id is zero")
	}
	if strings.TrimSpace(j.Prompt) == "" {
		return fmt.Errorf("cronjob: prompt is empty")
	}
	return nil
}

// normalize 统一 workdir 与表达式写法，保证同一条任务不会因 ""/"./" 等写法重复。
// 空 workdir 必须保持为空：filepath.Clean("") 会返回 "."，那样 Validate 就再也
// 拦不住"没填工作目录"的任务了。
func normalize(j Job) Job {
	// 简写（@daily 等）在这里翻译成标准 5 段表达式：数据库里只存原始写法，
	// 工具回显与 @cron 注入也就不会出现两种形态。
	j.Cron = ExpandCron(j.Cron)
	if wd := strings.TrimSpace(j.Workdir); wd != "" {
		j.Workdir = filepath.Clean(wd)
	} else {
		j.Workdir = ""
	}
	return j
}

// List 返回全部任务（按 id 升序）。
func (s *Store) List() ([]Job, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("cronjob: store is closed")
	}
	var jobs []Job
	if err := s.db.Order("id ASC").Find(&jobs).Error; err != nil {
		return nil, fmt.Errorf("cronjob: list jobs: %w", err)
	}
	return jobs, nil
}

// ListBySession 返回某个「工作目录 + 会话」下的任务（按 id 升序）。
//
// 供 cron 工具与 @cron 注入使用。workdir 用 filepath.Clean 规范化后比较，容忍
// "./x"、"x/" 之类的写法差异；会话 id 过滤交给 SQL。
func (s *Store) ListBySession(workdir string, sessionID uint32) ([]Job, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("cronjob: store is closed")
	}
	var jobs []Job
	if err := s.db.Where("session_id = ?", sessionID).Order("id ASC").Find(&jobs).Error; err != nil {
		return nil, fmt.Errorf("cronjob: list jobs of session %d: %w", sessionID, err)
	}
	wd := filepath.Clean(workdir)
	kept := make([]Job, 0, len(jobs))
	for _, j := range jobs {
		if filepath.Clean(j.Workdir) == wd {
			kept = append(kept, j)
		}
	}
	return kept, nil
}

// Get 按 id 查询任务；不存在时返回 gorm.ErrRecordNotFound。
func (s *Store) Get(id uint32) (Job, error) {
	var j Job
	if s == nil || s.db == nil {
		return j, fmt.Errorf("cronjob: store is closed")
	}
	if err := s.db.First(&j, id).Error; err != nil {
		return j, err
	}
	return j, nil
}

// Add 插入一条任务并返回带 id 的完整记录。
func (s *Store) Add(j Job) (Job, error) {
	j = normalize(j)
	if err := Validate(j); err != nil {
		return Job{}, err
	}
	j.ID = 0
	if err := s.db.Create(&j).Error; err != nil {
		return Job{}, fmt.Errorf("cronjob: add job: %w", err)
	}
	s.notify()
	return j, nil
}

// Update 按 id 覆盖一条任务的 cron/workdir/session_id/prompt/run_once。
func (s *Store) Update(j Job) error {
	j = normalize(j)
	if j.ID == 0 {
		return fmt.Errorf("cronjob: job id is zero")
	}
	if err := Validate(j); err != nil {
		return err
	}
	res := s.db.Model(&Job{}).Where("id = ?", j.ID).Updates(map[string]any{
		"cron":       j.Cron,
		"workdir":    j.Workdir,
		"session_id": j.SessionID,
		"prompt":     j.Prompt,
		"run_once":   j.RunOnce,
	})
	if res.Error != nil {
		return fmt.Errorf("cronjob: update job %d: %w", j.ID, res.Error)
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	s.notify()
	return nil
}

// Delete 删除一条任务。
func (s *Store) Delete(id uint32) error {
	if err := s.db.Delete(&Job{}, id).Error; err != nil {
		return fmt.Errorf("cronjob: delete job %d: %w", id, err)
	}
	s.notify()
	return nil
}

// DeleteMany 批量删除任务（清理用），返回删除行数。
func (s *Store) DeleteMany(ids []uint32) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := s.db.Where("id IN ?", ids).Delete(&Job{})
	if res.Error != nil {
		return 0, fmt.Errorf("cronjob: delete jobs %v: %w", ids, res.Error)
	}
	s.notify()
	return res.RowsAffected, nil
}

// Changed 返回任务变更通知通道。写入（Add/Update/Delete）后调度器据此重载内存表。
func (s *Store) Changed() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.changed
}

// notify 非阻塞地发出变更通知（通道容量 1，合并连续变更）。
func (s *Store) notify() {
	if s == nil || s.changed == nil {
		return
	}
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Close 关闭底层数据库连接（测试与显式停止用）。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	var err error
	s.closeOnce.Do(func() {
		var sqlDB interface{ Close() error }
		if sqlDB, err = s.db.DB(); err == nil {
			err = sqlDB.Close()
		}
	})
	return err
}
