package storage

import (
	"context"
	stdlog "log"
	"time"

	alog "github.com/cxykevin/alkaid0/log"

	gormLogger "gorm.io/gorm/logger"
)

var aLogger *alog.LogsObj

func init() {
	aLogger = alog.New("gorm")
}

// Level 定义 gorm 日志级别别名，用于内部比较
type Level gormLogger.LogLevel

// Logger 为 GORM 自定义 logger 实现
// 使用标准库 log 输出，并支持慢查询阈值与彩色输出
type Logger struct {
	slow      time.Duration
	stdLogger *stdlog.Logger
}

// New 创建日志器
func New() gormLogger.Interface {
	return &Logger{
		slow: time.Millisecond * 300,
	}
}

// LogMode 设置日志级别
func (l *Logger) LogMode(level gormLogger.LogLevel) gormLogger.Interface {
	return l
}

// Info 打印信息级别日志
func (l *Logger) Info(ctx context.Context, msg string, data ...any) {
	aLogger.Info(msg, data...)
}

// Warn 打印警告级别日志
func (l *Logger) Warn(ctx context.Context, msg string, data ...any) {
	aLogger.Warn(msg, data...)
}

// Error 打印错误级别日志
func (l *Logger) Error(ctx context.Context, msg string, data ...any) {
	aLogger.Error(msg, data...)
}

// Trace 跟踪 SQL 执行耗时与错误
func (l *Logger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {

	elapsed := time.Since(begin)
	sql, rows := fc()
	elapsedMs := float64(elapsed.Nanoseconds()) / 1e6

	// 错误优先级比慢查询与普通日志高
	if err != nil {
		if rows >= 0 {
			aLogger.Error("[%.3fms] rows:%d %s; error: %v", elapsedMs, rows, sql, err)
		} else {
			aLogger.Error("[%.3fms] %s; error: %v", elapsedMs, sql, err)
		}
		return
	}

	// 慢查询判定
	if l.slow > 0 && elapsed > l.slow {
		if rows >= 0 {
			aLogger.Debug("slow query > %s [%.3fms] %s", l.slow.String(), elapsedMs, sql)
		} else {
			aLogger.Debug("slow query > %s [%.3fms] rows:%d %s", l.slow.String(), elapsedMs, rows, sql)
		}
		return
	}

	// 普通查询日志
	if rows >= 0 {
		aLogger.Debug("[%.3fms] rows:%d %s", elapsedMs, rows, sql)
	} else {
		aLogger.Debug("[%.3fms] %s", elapsedMs, sql)
	}
}
