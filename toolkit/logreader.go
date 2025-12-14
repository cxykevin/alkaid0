package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// LogEntry 表示日志条目的结构
type LogEntry struct {
	Timestamp string
	Level     string
	Category  string
	Message   string
	Line      string
}

// Config 命令行参数的结构
type Config struct {
	FilePath string
	MinLevel string
	NoColor  bool
	Watch    bool // 监控模式标志
}

// 定义颜色常量
const (
	Reset   = "\033[0m"
	Bold    = "\033[1m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Magenta = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
)

var levelColors = map[string]string{
	"DEBUG": Cyan,
	"INFO":  Green,
	"WARN":  Yellow,
	"ERROR": Red,
}

var levelPriority = map[string]int{
	"DEBUG": 0,
	"INFO":  1,
	"WARN":  2,
	"ERROR": 3,
}

func parseConfig() Config {
	minLevel := flag.String("level", "DEBUG", "Minimum log level to display (DEBUG, INFO, WARN, ERROR)")
	noColor := flag.Bool("no-color", false, "Disable colored output")
	watch := flag.Bool("d", false, "Enable watch mode (monitor file changes)")
	flag.Parse()

	// 获取文件路径参数
	args := flag.Args()
	filePath := ""
	if len(args) > 0 {
		filePath = args[0]
	}

	return Config{
		FilePath: filePath,
		MinLevel: strings.ToUpper(*minLevel),
		NoColor:  *noColor,
		Watch:    *watch,
	}
}

func parseLogLine(line string) *LogEntry {
	// 匹配日志格式的正则表达式
	// 2025/12/07 14:04:35 [INFO][log] log inited
	re := regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2})\s+\[([A-Z]+)\]\[([^\]]+)\]\s+(.*)$`)

	matches := re.FindStringSubmatch(line)
	if len(matches) != 5 {
		return nil
	}

	return &LogEntry{
		Timestamp: matches[1],
		Level:     matches[2],
		Category:  matches[3],
		Message:   matches[4],
		Line:      line,
	}
}

func shouldDisplay(entry *LogEntry, minLevel string) bool {
	entryPriority, exists := levelPriority[entry.Level]
	if !exists {
		return true
	}

	minPriority, exists := levelPriority[minLevel]
	if !exists {
		return true
	}

	return entryPriority >= minPriority
}

func colorize(text, color string, noColor bool) string {
	if noColor {
		return text
	}
	return color + text + Reset
}

func highlightTimestamp(timestamp string, noColor bool) string {
	return colorize(timestamp, Blue, noColor)
}

func highlightLevel(level string, noColor bool) string {
	color := levelColors[level]
	if color == "" {
		color = White
	}
	return colorize("["+level+"]", color, noColor)
}

func highlightCategory(category string, noColor bool) string {
	return colorize("["+category+"]", Magenta, noColor)
}

func displayLogEntry(entry *LogEntry, config Config) {
	timestamp := highlightTimestamp(entry.Timestamp, config.NoColor)
	level := highlightLevel(entry.Level, config.NoColor)
	category := highlightCategory(entry.Category, config.NoColor)
	message := entry.Message

	fmt.Printf("%s %s %s %s\n", timestamp, level, category, message)
}

// readLogFile 读取整个日志文件
func readLogFile(filePath string) error {
	return readLogFileFrom(filePath, 0)
}

// readLogFileFrom 从指定行号开始读取日志文件，返回总行数和错误
func readLogFileFrom(filePath string, startLine int) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("无法打开文件: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		if lineNum <= startLine {
			continue
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		entry := parseLogLine(line)
		if entry == nil {
			if !config.NoColor {
				fmt.Printf("%s[LINE %d]%s 无法解析: %s\n", Yellow, lineNum, Reset, line)
			} else {
				fmt.Printf("[LINE %d] 无法解析: %s\n", lineNum, line)
			}
			continue
		}

		if shouldDisplay(entry, config.MinLevel) {
			displayLogEntry(entry, config)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取文件时出错: %v", err)
	}

	return nil
}

// clearScreen 清屏并清除滚动历史
func clearScreen() {
	fmt.Print("\033[2J\033[H\033[3J")
}

// // watchLogFile 实时监控日志文件变化
// func watchLogFile(filePath string, config Config) error {
// 	file, err := os.Open(filePath)
// 	if err != nil {
// 		return fmt.Errorf("无法打开文件: %v", err)
// 	}
// 	defer file.Close()

// 	// 获取初始文件信息
// 	info, err := file.Stat()
// 	if err != nil {
// 		return fmt.Errorf("无法获取文件信息: %v", err)
// 	}

// 	lastSize := info.Size()
// 	lastModTime := info.ModTime()

// 	for {
// 		time.Sleep(500 * time.Millisecond) // 每500毫秒检查一次

// 		// 重新获取文件信息
// 		newInfo, err := os.Stat(filePath)
// 		if err != nil {
// 			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
// 			continue
// 		}

// 		// 检查文件是否被清空
// 		if newInfo.Size() == 0 && lastSize > 0 {
// 			fmt.Println("\033[2J\033[H") // 清屏
// 			fmt.Println("文件已清空，显示新内容...")
// 			lastSize = 0
// 			lastModTime = newInfo.ModTime()
// 			continue
// 		}

// 		// 检查文件是否有新内容
// 		if newInfo.Size() > lastSize || newInfo.ModTime().After(lastModTime) {
// 			// 清屏
// 			fmt.Print("\033[2J\033[H")

// 			// 重新读取整个文件
// 			if err := readLogFile(filePath); err != nil {
// 				fmt.Fprintf(os.Stderr, "错误: %v\n", err)
// 				continue
// 			}

// 			lastSize = newInfo.Size()
// 			lastModTime = newInfo.ModTime()
// 		}

// 		// 检查文件是否被截断（大小变小）
// 		if newInfo.Size() < lastSize {
// 			fmt.Println("\033[2J\033[H") // 清屏
// 			fmt.Println("文件被截断，重新加载...")
// 			lastSize = newInfo.Size()
// 			lastModTime = newInfo.ModTime()
// 		}
// 	}
// }

func displayUsage() {
	fmt.Println("用法: logreader [选项] <日志文件路径>")
	fmt.Println("")
	fmt.Println("选项:")
	fmt.Println("  -level <级别>    最小显示级别 (DEBUG, INFO, WARN, ERROR)")
	fmt.Println("  -no-color        禁用彩色输出")
	fmt.Println("  -d               启用监控模式 (监控文件变化)")
	fmt.Println("  -h, --help       显示此帮助信息")
	fmt.Println("")
	fmt.Println("示例:")
	fmt.Println("  logreader app.log")
	fmt.Println("  logreader -level INFO app.log")
	fmt.Println("  logreader -no-color -level ERROR app.log")
}

var config Config

func main() {
	config = parseConfig()

	// 显示帮助信息
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		displayUsage()
		return
	}

	if config.FilePath == "" {
		fmt.Fprintln(os.Stderr, "错误: 请提供日志文件路径")
		displayUsage()
		os.Exit(1)
	}

	if err := readLogFile(config.FilePath); err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}

	// 如果启用监控模式，则开始监控文件变化
	if config.Watch {
		watcher := newFileWatcher(config.FilePath, config)
		watcher.initState()
		go watcher.watch()

		// 让主线程等待，避免退出
		select {}
	}
}

// FileWatcher 用于监控文件变化
type FileWatcher struct {
	filePath    string
	lastSize    int64
	lastModTime time.Time
	lastLine    int
	config      Config
}

func newFileWatcher(filePath string, config Config) *FileWatcher {
	return &FileWatcher{
		filePath: filePath,
		config:   config,
	}
}

// initState 初始化监控器状态
func (fw *FileWatcher) initState() {
	info, err := os.Stat(fw.filePath)
	if err != nil {
		return
	}
	fw.lastSize = info.Size()
	fw.lastModTime = info.ModTime()
	fw.lastLine = countLines(fw.filePath)
}

func (fw *FileWatcher) watch() {
	for {
		time.Sleep(500 * time.Millisecond)

		info, err := os.Stat(fw.filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			return
		}

		// 文件被截断，清屏重新输出全部
		if info.Size() < fw.lastSize {
			clearScreen()
			fw.lastLine = 0
			if err := readLogFile(fw.filePath); err != nil {
				fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			}
			fw.lastSize = info.Size()
			fw.lastModTime = info.ModTime()
			continue
		}

		// 文件有新内容，增量输出
		if info.Size() > fw.lastSize || info.ModTime().After(fw.lastModTime) {
			if err := readLogFileFrom(fw.filePath, fw.lastLine); err != nil {
				fmt.Fprintf(os.Stderr, "错误: %v\n", err)
			} else {
				fw.lastLine = countLines(fw.filePath)
			}
			fw.lastSize = info.Size()
			fw.lastModTime = info.ModTime()
		}
	}
}

// countLines 统计文件总行数
func countLines(filePath string) int {
	file, err := os.Open(filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count
}
