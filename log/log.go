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

// logWorker 异步日志处理worker
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

func Shutdown() {
	if !loggerInited {
		return
	}
	atomic.StoreUint32(&isShutdown, 1)
	flushLogs()
	close(logChannel)
}

type LogsObj struct {
	moduleName string
}

func (l *LogsObj) log(level string, msg string, v ...any) {
	str := fmt.Sprintf(msg, v...)
	str = SanitizeSensitiveInfo(str)
	str = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(
		str,
		"\\", "\\\\"),
		"\n", "\\n"),
		"\r", "\\r"),
		"\t", "\\t")

	if atomic.LoadUint32(&isShutdown) == 1 {
		l.logSync(level, "%s", str)
		return
	}

	// 异步写入日志
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
		logWaitGroup.Done()
		atomic.AddUint64(&droppedLogCount, 1)
		l.logSync("WARN", "log channel full, drop log (total dropped: %d)", atomic.LoadUint64(&droppedLogCount))
	}
}

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
	l.log("INFO", msg, v...)
}

// Warn 打印警告
func (l *LogsObj) Warn(msg string, v ...any) {
	l.log("WARN", msg, v...)
}

// Error 打印错误 - 强制同步写入
func (l *LogsObj) Error(msg string, v ...any) {
	// 先flush所有pending的日志
	flushLogs()
	// 然后同步写入error日志
	l.logSync("ERROR", msg, v...)
}

// Debug 打印调试
func (l *LogsObj) Debug(msg string, v ...any) {
	l.log("DEBUG", msg, v...)
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
