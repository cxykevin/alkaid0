package log

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxykevin/alkaid0/config/structs"
)

// GlobalConfig 配置文件对象
var GlobalConfig = &structs.Config{}

const defaultLogPath = "~/.config/alkaid0/log.log"
const envLogName = "ALKAID0_LOG_PATH"

var logPath string

// Logger 日志对象
var Logger *log.Logger

var loggerInited bool = false

// ExpandPath 展开路径中的 ~ 和环境变量
func ExpandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		// 获取用户家目录
		homeDir, err := os.UserHomeDir()
		if err == nil {
			path = homeDir + path[1:]
		}
	}
	// 展开环境变量
	return os.ExpandEnv(path)
}

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
	expandedPath := ExpandPath(logPath)

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

	loggerInited = true

	sysObj := New("log")
	sysObj.Info("log inited")

	// logLck.Unlock()

}

// LogsObj 日志对象
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

// Error 打印错误
func (l *LogsObj) Error(msg string, v ...any) {
	l.log("ERROR", msg, v...)
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
