package cronjob

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStoreCreatesSchemaAndVersion(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(filepath.Join(dir, DBFileName)); err != nil {
		t.Fatalf("cron.sqlite not created: %v", err)
	}
	got, err := s.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if got != schemaVersion {
		t.Fatalf("schema version = %q, want %q", got, schemaVersion)
	}
	// 表名固定为 cron，列与约定一致。
	if !s.db.Migrator().HasTable("cron") {
		t.Fatalf("table cron is missing")
	}
	for _, col := range []string{"id", "cron", "workdir", "session_id", "prompt", "run_once"} {
		if !s.db.Migrator().HasColumn(&Job{}, col) {
			t.Fatalf("column %s is missing", col)
		}
	}
}

func TestStoreCRUD(t *testing.T) {
	s := testStore(t)

	job, err := s.Add(Job{Cron: " */5 * * * * ", Workdir: "/tmp/../tmp/work", SessionID: 7, Prompt: "check status", RunOnce: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if job.ID == 0 {
		t.Fatalf("Add did not assign an id")
	}
	if job.Cron != "*/5 * * * *" {
		t.Fatalf("cron not trimmed: %q", job.Cron)
	}
	// 用 filepath.Clean 现算期望值：Windows 会把 /tmp/../tmp/work 规范化成 \tmp\work。
	if want := filepath.Clean("/tmp/../tmp/work"); job.Workdir != want {
		t.Fatalf("workdir not cleaned: %q, want %q", job.Workdir, want)
	}

	jobs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("List = %#v, want single job %d", jobs, job.ID)
	}
	if !jobs[0].RunOnce {
		t.Fatalf("run_once not persisted: %#v", jobs[0])
	}

	job.Prompt = "check status twice"
	job.SessionID = 8
	job.RunOnce = false
	if err := s.Update(job); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := s.Get(job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Prompt != "check status twice" || got.SessionID != 8 {
		t.Fatalf("Get = %#v, want updated prompt/session", got)
	}
	if got.RunOnce {
		t.Fatalf("Update did not clear run_once: %#v", got)
	}

	if err := s.Update(Job{ID: 9999, Cron: "* * * * *", Workdir: "/tmp", SessionID: 1, Prompt: "x"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Update missing job err = %v, want ErrRecordNotFound", err)
	}

	if err := s.Delete(job.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(job.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Get after delete err = %v, want ErrRecordNotFound", err)
	}
}

func TestStoreValidation(t *testing.T) {
	s := testStore(t)

	ok := Job{Cron: "0 9 * * *", Workdir: "/tmp", SessionID: 1, Prompt: "hello"}
	if err := Validate(ok); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	cases := []struct {
		name string
		job  Job
	}{
		{"empty cron", Job{Workdir: "/tmp", SessionID: 1, Prompt: "x"}},
		{"bad cron", Job{Cron: "not a cron", Workdir: "/tmp", SessionID: 1, Prompt: "x"}},
		{"6 fields", Job{Cron: "* * * * * *", Workdir: "/tmp", SessionID: 1, Prompt: "x"}},
		{"empty workdir", Job{Cron: "* * * * *", SessionID: 1, Prompt: "x"}},
		{"zero session", Job{Cron: "* * * * *", Workdir: "/tmp", Prompt: "x"}},
		{"empty prompt", Job{Cron: "* * * * *", Workdir: "/tmp", SessionID: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.job); err == nil {
				t.Fatalf("Validate(%#v) = nil, want error", c.job)
			}
			if _, err := s.Add(c.job); err == nil {
				t.Fatalf("Add(%#v) = nil error, want rejection", c.job)
			}
		})
	}

	// @daily 等预定义描述符可用。
	if err := Validate(Job{Cron: "@daily", Workdir: "/tmp", SessionID: 1, Prompt: "x"}); err != nil {
		t.Fatalf("predefined descriptor rejected: %v", err)
	}
}

// TestExpandCronShorthand 简写语法统一翻译为 5 段表达式；普通写法只去空白。
func TestExpandCronShorthand(t *testing.T) {
	cases := map[string]string{
		"@hourly":       "0 * * * *",
		"@daily":        "0 0 * * *",
		"@midnight":     "0 0 * * *",
		"@weekly":       "0 0 * * 0",
		"@monthly":      "0 0 1 * *",
		"@yearly":       "0 0 1 1 *",
		"@annually":     "0 0 1 1 *",
		"  @DAILY ":     "0 0 * * *",
		"0 9 * * *":     "0 9 * * *",
		" 0 9 * * 1-5 ": "0 9 * * 1-5",
	}
	for in, want := range cases {
		if got := ExpandCron(in); got != want {
			t.Fatalf("ExpandCron(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestAddStoresExpandedShorthand 简写在写入时翻译：库里与回读都是 5 段表达式。
func TestAddStoresExpandedShorthand(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()
	job, err := s.Add(Job{Cron: "@weekly", Workdir: dir, SessionID: 1, Prompt: "x"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if job.Cron != "0 0 * * 0" {
		t.Fatalf("Add returned cron %q, want expanded 5-field form", job.Cron)
	}
	reloaded, err := s.Get(job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.Cron != "0 0 * * 0" {
		t.Fatalf("stored cron = %q, want expanded 5-field form", reloaded.Cron)
	}

	// Update 同样翻译简写。
	job.Cron = "@hourly"
	if err := s.Update(job); err != nil {
		t.Fatalf("Update: %v", err)
	}
	reloaded, err = s.Get(job.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if reloaded.Cron != "0 * * * *" {
		t.Fatalf("updated cron = %q, want %q", reloaded.Cron, "0 * * * *")
	}
}

// TestListBySession 只返回同一 workdir + 会话的任务，workdir 写法差异被规范化。
func TestListBySession(t *testing.T) {
	s := testStore(t)
	dir := t.TempDir()
	other := t.TempDir()

	mine, err := s.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 7, Prompt: "mine"})
	if err != nil {
		t.Fatalf("Add mine: %v", err)
	}
	if _, err := s.Add(Job{Cron: "* * * * *", Workdir: dir, SessionID: 8, Prompt: "other session"}); err != nil {
		t.Fatalf("Add other session: %v", err)
	}
	if _, err := s.Add(Job{Cron: "* * * * *", Workdir: other, SessionID: 7, Prompt: "other workdir"}); err != nil {
		t.Fatalf("Add other workdir: %v", err)
	}

	jobs, err := s.ListBySession(filepath.Join(dir, "."), 7)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != mine.ID {
		t.Fatalf("ListBySession = %#v, want only job %d", jobs, mine.ID)
	}

	// 空结果不是错误。
	jobs, err = s.ListBySession(dir, 99)
	if err != nil {
		t.Fatalf("ListBySession empty: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("ListBySession(unknown session) = %#v, want empty", jobs)
	}
}

// TestOpenMigratesLegacyTableWithoutVersionBump 旧库（无 run_once 列）由 AutoMigrate
// 自动补列，版本号保持 1：纯加列不需要升版本。
func TestOpenMigratesLegacyTableWithoutVersionBump(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, DBFileName)

	legacy, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	stmts := []string{
		`CREATE TABLE cron (id INTEGER PRIMARY KEY AUTOINCREMENT, cron TEXT NOT NULL, workdir TEXT NOT NULL, session_id INTEGER NOT NULL, prompt TEXT NOT NULL)`,
		`CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT, updated_at datetime)`,
		`INSERT INTO metadata(key, value) VALUES ('schema_version', '1')`,
		`INSERT INTO cron(cron, workdir, session_id, prompt) VALUES ('* * * * *', '/tmp', 1, 'legacy job')`,
	}
	for _, stmt := range stmts {
		if err := legacy.Exec(stmt).Error; err != nil {
			t.Fatalf("prepare legacy db: %v", err)
		}
	}
	sqlDB, _ := legacy.DB()
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer s.Close()

	if !s.db.Migrator().HasColumn(&Job{}, "run_once") {
		t.Fatalf("run_once column was not added to the legacy table")
	}
	got, err := s.Get(1)
	if err != nil {
		t.Fatalf("Get legacy row: %v", err)
	}
	if got.Prompt != "legacy job" || got.RunOnce {
		t.Fatalf("legacy row = %#v, want prompt kept and run_once false", got)
	}
	if v, err := s.SchemaVersion(); err != nil || v != schemaVersion {
		t.Fatalf("schema version = %q (err %v), want %q", v, err, schemaVersion)
	}
}

func TestStorePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	job, err := s.Add(Job{Cron: "0 * * * *", Workdir: "/tmp", SessionID: 42, Prompt: "persist me"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.Get(job.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Prompt != "persist me" || got.SessionID != 42 {
		t.Fatalf("reloaded job = %#v, want persisted values", got)
	}
}
