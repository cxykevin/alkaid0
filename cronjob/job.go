// Package cronjob 实现由 cron 表达式驱动的持久化定时任务。
//
// 存储位置：alkaid0 配置目录下的 cron.sqlite（与 config.json 同级），启动时打开并
// 常驻保持连接；每条任务由 cron 表达式、工作目录、会话 ID、提示词与一次性标记组成，
// 触发时把提示词作为一条普通用户消息投递到对应会话（见 server/actions/cronjob.go）。
//
// 阶段一范围：存储 + 调度 + 清理。工具与 RPC 协议由后续阶段实现，存储层已提供
// 完整的 CRUD 与变更通知，供工具层直接复用。
package cronjob

import "time"

const (
	// DBFileName 定时任务数据库文件名（位于配置目录）。
	DBFileName = "cron.sqlite"
	// LockFileName 独占锁文件名，保证同一配置目录只有一个进程运行调度器。
	LockFileName = "cron.sqlite.lock"
	// Prefix 触发时加在提示词前，便于客户端与模型识别消息来源。
	Prefix = "[cronjob] "
)

// schemaVersion 当前 cron.sqlite 的 schema 版本号，写入 metadata 表。
//
// 版本号机制与主库（storage/migrate）一致：metadata 表中 key=schema_version 的
// 值记录结构版本。**只有 AutoMigrate 无法完成的迁移才需要升版本号**；纯加列
// （如 run_once）由 AutoMigrate 自动补齐，版本号保持不变。
const schemaVersion = "1"

const schemaVersionKey = "schema_version"

// Job 一条定时任务（表名 cron）。
//
// 列：id 主键 + cron 表达式 + 工作目录 + 会话 ID + 提示词 + 一次性标记。
// 刻意不添加时间戳列：任务的有效性由清理逻辑（工作目录/会话是否存在）判定，
// 调度所需的下一次触发时间只保存在内存中。
type Job struct {
	ID        uint32 `gorm:"column:id;primaryKey;autoIncrement"`
	Cron      string `gorm:"column:cron;not null"`
	Workdir   string `gorm:"column:workdir;not null"`
	SessionID uint32 `gorm:"column:session_id;not null"`
	Prompt    string `gorm:"column:prompt;not null"`
	// RunOnce 一次性任务：投递成功后立即删除该行；投递失败则保留，等下一个
	// 触发点再试（失效目标由 12h 清理兜底删除）。
	RunOnce bool `gorm:"column:run_once;not null;default:false"`
}

// TableName 固定表名为 cron（小写单数）。
func (Job) TableName() string { return "cron" }

// metadata cron.sqlite 的元信息表（与主库同名同结构，记录 schema 版本）。
type metadata struct {
	Key       string `gorm:"column:key;primaryKey"`
	Value     string `gorm:"column:value"`
	UpdatedAt time.Time
}

// TableName 固定表名为 metadata，避免 GORM 复数化。
func (metadata) TableName() string { return "metadata" }
