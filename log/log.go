// Package log 日志模块
package log

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/internal/configutil"
)

var globalLogLevel = 1

// GlobalConfig 配置文件对象
var GlobalConfig = &structs.Config{}

const defaultLogPath = "~/.config/alkaid0/log.log"
const envLogName = "ALKAID0_LOG_PATH"

var logPath string

// Logger 日志对象
var Logger *log.Logger

var loggerInited bool = false

// 异步日志相关
type logMessage struct {
	level      string
	moduleName string
	message    string
}

var logChannel chan logMessage
var logWaitGroup sync.WaitGroup
var logFlushMutex sync.Mutex
var droppedLogCount uint64
var isShutdown uint32

// var logLck sync.Mutex

// Load 加载配置文件
func Load() {
	if loggerInited {
		return
	}
	if v := os.Getenv("ALKAID0_LOG_LEVEL"); v != "" {
		switch v {
		case "debug":
			globalLogLevel = 0
		case "info":
			globalLogLevel = 1
		case "warn":
			globalLogLevel = 2
		case "error":
			globalLogLevel = 3
		}
	}
	// logLck.Lock()
	// 读取环境变量
	if path := os.Getenv(envLogName); path != "" {
		logPath = path
	} else {
		logPath = defaultLogPath
	}

	// 展开用户目录路径
	expandedPath := configutil.ExpandPath(logPath)

	// 确保目录存在
	dir := filepath.Dir(expandedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		// 目录创建失败，使用默认配置
		return
	}

	// 新建/清空日志
	if _, err := os.Create(expandedPath); err != nil {
		// 直接 panic
		panic(err)
	}

	// 打开日志文件
	file, err := os.OpenFile(expandedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// 直接 panic
		panic(err)
	}

	// 创建logger，输出到文件
	Logger = log.New(file, "", log.LstdFlags)

	// 初始化异步日志channel
	logChannel = make(chan logMessage, 1000) // 缓冲1000条日志

	// 启动日志处理goroutine
	go logWorker()

	loggerInited = true

	sysObj := New("log")
	sysObj.Info("log inited")

	// logLck.Unlock()

}

// logWorker 异步日志处理 worker goroutine。
// 后台循环读取 logChannel，将日志逐条同步写入文件。
// 使用缓冲通道（容量 1000）解耦日志调用方和 I/O 写入方，
// 防止主程序在日志写入时阻塞。
// 通道关闭时 goroutine 自动退出。
func logWorker() {
	for msg := range logChannel {
		str := fmt.Sprintf("[%s][%s] %s", msg.level, msg.moduleName, msg.message)
		Logger.Println(str)
		logWaitGroup.Done()
	}
}

// flushLogs 等待所有pending的日志写入完成
func flushLogs() {
	logFlushMutex.Lock()
	defer logFlushMutex.Unlock()
	logWaitGroup.Wait()
}

// Shutdown 关闭日志模块
func Shutdown() {
	if !loggerInited {
		return
	}
	atomic.StoreUint32(&isShutdown, 1)
	flushLogs()
	close(logChannel)
}

// LogsObj 日志对象
type LogsObj struct {
	moduleName string
}

// log 核心日志写入方法。
// 设计要点：
//  1. 先脱敏（SanitizeSensitiveInfo）再写入，确保密钥等敏感信息不被记录
//  2. 将多行内容转义为单行（\n→\\n 等），保持日志文件格式整洁
//  3. 关闭阶段（isShutdown）降级为同步写入，避免通道关闭后 panic
//  4. 正常运行时使用有缓冲通道异步写入，select-default 在通道满时丢弃日志
//     防止高频日志拖慢主程序
func (l *LogsObj) log(level string, msg string, v ...any) {
	str := fmt.Sprintf(msg, v...)
	// 自动脱敏 API 密钥等敏感信息，避免日志泄露
	str = SanitizeSensitiveInfo(str)
	// 转义特殊字符保持日志单行格式，便于后续 grep/awk 处理
	str = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(
		str,
		"\\", "\\\\"),
		"\n", "\\n"),
		"\r", "\\r"),
		"\t", "\\t")

	// isShutdown 标志下改用同步写入。
	// 原因：关闭期间 logWorker 可能已退出，通道接收会 panic。
	// 此时应优先保证关键日志被写入，即使阻塞应用程序。
	if atomic.LoadUint32(&isShutdown) == 1 {
		l.logSync(level, "%s", str)
		return
	}

	// 异步写入日志：将日志消息发送到缓冲通道，由 logWorker 负责实际 I/O
	logFlushMutex.Lock()
	logWaitGroup.Add(1)
	logFlushMutex.Unlock()

	select {
	case logChannel <- logMessage{
		level:      level,
		moduleName: l.moduleName,
		message:    str,
	}:
	default:
		// 通道已满时丢弃日志并计数，防止日志阻塞主程序
		// 同时同步回写一条 WARN 日志作为预警
		logWaitGroup.Done()
		atomic.AddUint64(&droppedLogCount, 1)
		l.logSync("WARN", "log channel full, drop log (total dropped: %d)", atomic.LoadUint64(&droppedLogCount))
	}
}

// logSync 同步日志写入方法，绕开异步通道直接写入日志文件。
// 在关闭阶段（isShutdown）或通道满载时作为 fallback 使用。
// 同步写入虽会阻塞，但能保证日志不丢失。
func (l *LogsObj) logSync(level string, msg string, v ...any) {
	str := fmt.Sprintf(msg, v...)
	str = SanitizeSensitiveInfo(str)
	str = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(
		str,
		"\\", "\\\\"),
		"\n", "\\n"),
		"\r", "\\r"),
		"\t", "\\t")

	// 同步写入日志
	Logger.Printf("[%s][%s] %s", level, l.moduleName, str)
}

// Info 打印日志
func (l *LogsObj) Info(msg string, v ...any) {
	if globalLogLevel <= 1 {
		l.log("INFO", msg, v...)
	}
}

// Warn 打印警告
func (l *LogsObj) Warn(msg string, v ...any) {
	if globalLogLevel <= 2 {
		l.log("WARN", msg, v...)
	}
}

// Error 打印错误 - 强制同步写入
func (l *LogsObj) Error(msg string, v ...any) {
	if globalLogLevel <= 3 {
		// 先flush所有pending的日志
		flushLogs()
		// 然后同步写入error日志
		l.logSync("ERROR", msg, v...)
	}
}

// Debug 打印调试
func (l *LogsObj) Debug(msg string, v ...any) {
	if globalLogLevel <= 0 {
		l.log("DEBUG", msg, v...)
	}
}

// New 创建日志对象
func New(moduleName string) *LogsObj {
	// logLck.Lock()
	// logLck.Unlock()
	if !loggerInited {
		Load()
	}
	return &LogsObj{moduleName: moduleName}
}
