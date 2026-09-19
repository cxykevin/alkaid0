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

// Level gorm 日志级别别名，用于内部日志级别比较与设置
type Level gormLogger.LogLevel

// Logger 为 GORM 自定义 logger 实现
// 使用标准库 log 输出，并支持慢查询阈值与彩色输出
type Logger struct {
	slow      time.Duration
	stdLogger *stdlog.Logger
	level     gormLogger.LogLevel
}

// New 创建 GORM 自定义日志器，默认慢查询阈值为 300ms
func New() gormLogger.Interface {
	return &Logger{
		slow:  time.Millisecond * 300,
		level: gormLogger.Info,
	}
}

// LogMode 设置日志级别（Silent 时抑制全部 SQL 输出）
func (l *Logger) LogMode(level gormLogger.LogLevel) gormLogger.Interface {
	l.level = level
	return l
}

// Info 打印信息级别日志
func (l *Logger) Info(ctx context.Context, msg string, data ...any) {
	if l.level >= gormLogger.Info {
		aLogger.Info(msg, data...)
	}
}

// Warn 打印警告级别日志
func (l *Logger) Warn(ctx context.Context, msg string, data ...any) {
	if l.level >= gormLogger.Warn {
		aLogger.Warn(msg, data...)
	}
}

// Error 打印错误级别日志
func (l *Logger) Error(ctx context.Context, msg string, data ...any) {
	if l.level >= gormLogger.Error {
		aLogger.Error(msg, data...)
	}
}

// ParamsFilter 实现 gorm.ParamsFilter 接口：把参数从日志用的 SQL 中剔除。
//
// 为什么必须实现：GORM 交给 logger 的 SQL 是经 Dialector.Explain 插值后的文本，
// 参数值会被原样写进日志——包括 key_mappings 里的原始密钥、messages 里的完整
// 提示词、refer_files 里的文件内容；而日志尾部会被 /feedback 上传
// （server/actions/feedback.go）。返回 nil 参数后 GORM 只渲染占位符
// （等效官方 ParameterizedQueries 选项），排查问题时语句/耗时/行数仍然可见。
//
// 本方法不受 LogMode 影响：DEBUG 级别同样只输出占位符。
func (l *Logger) ParamsFilter(_ context.Context, sql string, _ ...interface{}) (string, []interface{}) {
	return sql, nil
}

// Trace 跟踪 SQL 执行耗时与错误
func (l *Logger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level == gormLogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()
	elapsedMs := float64(elapsed.Nanoseconds()) / 1e6

	// 错误优先级比慢查询与普通日志高
	if err != nil {
		if l.level >= gormLogger.Error {
			if rows >= 0 {
				aLogger.Error("[%.3fms] rows:%d %s; error: %v", elapsedMs, rows, sql, err)
			} else {
				aLogger.Error("[%.3fms] %s; error: %v", elapsedMs, sql, err)
			}
		}
		return
	}

	// 慢查询判定
	if l.slow > 0 && elapsed > l.slow {
		if l.level >= gormLogger.Warn {
			if rows >= 0 {
				aLogger.Debug("slow query > %s [%.3fms] rows:%d %s", l.slow.String(), elapsedMs, rows, sql)
			} else {
				aLogger.Debug("slow query > %s [%.3fms] %s", l.slow.String(), elapsedMs, sql)
			}
		}
		return
	}

	// 普通查询日志
	if l.level >= gormLogger.Info {
		if rows >= 0 {
			aLogger.Debug("[%.3fms] rows:%d %s", elapsedMs, rows, sql)
		} else {
			aLogger.Debug("[%.3fms] %s", elapsedMs, sql)
		}
	}
}
