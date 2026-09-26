package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 本文件补充 logreader.go 的测试。
//
// 大部分逻辑在进程内直接调用；只有两处例外改用“以子进程方式重跑本测试二进制”驱动：
//   - main() 中部分分支以 os.Exit 结束进程，进程内调用会直接终止测试；
//   - FileWatcher.watch() 是永不返回的监控循环，进程内调用会遗留一个持续写 stdout 的
//     goroutine，与 go test 自身的输出竞争（-race 下会被判定为数据竞争）。
const (
	// childEnv 标记当前进程是由父测试驱动的子进程，值为待执行场景名
	childEnv = "ALKAID0_TEST_LOGREADER_CHILD"
	// childFileEnv 由父测试传递，指向子进程需要操作的日志文件
	childFileEnv = "ALKAID0_TEST_LOGREADER_FILE"

	// childCLITest / childWatchTest 是子进程要执行的测试名
	childCLITest   = "TestLogreaderCLIChild"
	childWatchTest = "TestLogreaderWatchChild"

	// pollWait 略大于 watch() 内部的 500ms 轮询间隔，保证每个阶段至少触发一轮检查
	pollWait = time.Second
)

// ---------- 辅助函数 ----------

// withConfig 临时替换包级 config（readLogFileFrom / readLogDelta 依赖它），测试结束后还原
func withConfig(t *testing.T, cfg Config) {
	t.Helper()
	old := config
	config = cfg
	t.Cleanup(func() { config = old })
}

// captureStdout 捕获 fn 执行期间写入 os.Stdout 的内容，仅用于没有其它 goroutine 写 stdout 的测试
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建管道失败: %v", err)
	}

	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	return out
}

// parseConfigWith 在隔离的 FlagSet 下调用 parseConfig，避免重复调用触发 flag 重复注册 panic
func parseConfigWith(t *testing.T, args ...string) Config {
	t.Helper()

	oldArgs, oldFlagSet := os.Args, flag.CommandLine
	flag.CommandLine = flag.NewFlagSet("logreader-test", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = append([]string{"logreader"}, args...)
	defer func() {
		os.Args, flag.CommandLine = oldArgs, oldFlagSet
	}()

	return parseConfig()
}

// writeTempLog 把若干行写入临时日志文件并返回路径
func writeTempLog(t *testing.T, lines ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "app.log")
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入测试日志失败: %v", err)
	}
	return path
}

// writeHugeLineLog 写入一行超过 bufio.Scanner 上限（64KB）的日志，用于触发 scanner.Err()
func writeHugeLineLog(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "huge.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 70*1024)+"\n"), 0644); err != nil {
		t.Fatalf("写入超长行日志失败: %v", err)
	}
	return path
}

// appendLogLine 以追加方式写入一行日志
func appendLogLine(t *testing.T, path, line string) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("打开日志文件失败: %v", err)
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		t.Fatalf("追加日志失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭日志文件失败: %v", err)
	}
}

// runChildAsLogreader 以子进程方式重跑本测试二进制，驱动指定场景，返回 stdout / stderr / 退出码
func runChildAsLogreader(t *testing.T, testName, scenario, file string) (string, string, int) {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("获取测试二进制路径失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(), childEnv+"="+scenario, childFileEnv+"="+file)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("运行子进程失败: %v", err)
		}
		if ctx.Err() != nil {
			t.Fatalf("子进程场景 %s 超时未结束", scenario)
		}
		code = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// ---------- parseConfig ----------

func TestParseConfig(t *testing.T) {
	def := parseConfigWith(t)
	if def.FilePath != "" || def.MinLevel != "DEBUG" || def.NoColor || def.Watch {
		t.Errorf("默认配置 = %+v, want {FilePath:\"\" MinLevel:DEBUG NoColor:false Watch:false}", def)
	}

	got := parseConfigWith(t, "-level", "info", "-no-color", "-d", "/tmp/app.log")
	if got.FilePath != "/tmp/app.log" {
		t.Errorf("FilePath = %q, want /tmp/app.log", got.FilePath)
	}
	if got.MinLevel != "INFO" {
		t.Errorf("MinLevel = %q, want INFO（-level 应大写归一）", got.MinLevel)
	}
	if !got.NoColor || !got.Watch {
		t.Errorf("NoColor/Watch = %v/%v, want true/true", got.NoColor, got.Watch)
	}
}

// ---------- readLogFile / readLogFileFrom ----------

func TestReadLogFileFromBranches(t *testing.T) {
	path := writeTempLog(t,
		"2025/12/07 14:04:35 [INFO][log] log inited",
		"无法解析的一行",
		"",
		"2025/12/07 14:04:36 [DEBUG][log] debug msg",
	)

	// NoColor=true：级别过滤 + 无颜色提示无法解析的行；空行直接跳过
	withConfig(t, Config{MinLevel: "INFO", NoColor: true})
	out := captureStdout(t, func() {
		if err := readLogFile(path); err != nil {
			t.Errorf("readLogFile 返回错误: %v", err)
		}
	})
	if !strings.Contains(out, "log inited") {
		t.Errorf("应输出 INFO 行, got %q", out)
	}
	if !strings.Contains(out, "[LINE 2] 无法解析: 无法解析的一行") {
		t.Errorf("应输出无颜色提示, got %q", out)
	}
	if strings.Contains(out, "debug msg") {
		t.Errorf("MinLevel=INFO 不应输出 DEBUG 行, got %q", out)
	}
	if strings.Contains(out, "[LINE 3]") {
		t.Errorf("空行应被跳过, got %q", out)
	}

	// startLine 跳过前 N 行（MinLevel=DEBUG 才能看到第 4 行的 DEBUG 日志）
	withConfig(t, Config{MinLevel: "DEBUG", NoColor: true})
	out = captureStdout(t, func() {
		if err := readLogFileFrom(path, 3); err != nil {
			t.Errorf("readLogFileFrom 返回错误: %v", err)
		}
	})
	if strings.Contains(out, "log inited") || strings.Contains(out, "无法解析") {
		t.Errorf("startLine=3 应跳过前 3 行, got %q", out)
	}
	if !strings.Contains(out, "debug msg") {
		t.Errorf("startLine=3 应输出第 4 行, got %q", out)
	}

	// NoColor=false：带颜色提示，且 MinLevel=DEBUG 时 DEBUG 行也会输出
	withConfig(t, Config{MinLevel: "DEBUG", NoColor: false})
	out = captureStdout(t, func() {
		if err := readLogFileFrom(path, 1); err != nil {
			t.Errorf("readLogFileFrom 返回错误: %v", err)
		}
	})
	if !strings.Contains(out, Yellow+"[LINE 2]"+Reset) {
		t.Errorf("应输出带颜色的提示, got %q", out)
	}
	if !strings.Contains(out, "debug msg") {
		t.Errorf("MinLevel=DEBUG 应输出 DEBUG 行, got %q", out)
	}
}

func TestReadLogFileErrors(t *testing.T) {
	withConfig(t, Config{MinLevel: "DEBUG", NoColor: true})

	err := readLogFile(filepath.Join(t.TempDir(), "missing.log"))
	if err == nil || !strings.Contains(err.Error(), "无法打开文件") {
		t.Errorf("读取不存在的文件 err = %v, want 包含“无法打开文件”", err)
	}

	err = readLogFile(writeHugeLineLog(t))
	if err == nil || !strings.Contains(err.Error(), "读取文件时出错") {
		t.Errorf("超长行 err = %v, want 包含“读取文件时出错”", err)
	}
}

// TestShouldDisplayUnknownLevels 覆盖级别表未命中时的兜底分支
func TestShouldDisplayUnknownLevels(t *testing.T) {
	// 日志级别不在 levelPriority 中：按可显示处理
	if !shouldDisplay(&LogEntry{Level: "TRACE"}, "DEBUG") {
		t.Error("未知日志级别应默认显示")
	}

	// -level 不在 levelPriority 中：不做过滤，全部显示
	if !shouldDisplay(&LogEntry{Level: "ERROR"}, "TRACE") {
		t.Error("未知 -level 应默认显示")
	}
}

// ---------- readLogDelta ----------

func TestReadLogDelta(t *testing.T) {
	path := writeTempLog(t,
		"2025/12/07 14:04:35 [INFO][log] first line",
		"无法解析的一行",
	)
	withConfig(t, Config{MinLevel: "DEBUG", NoColor: true})

	var offset int64
	var lineNum int
	out := captureStdout(t, func() {
		var err error
		offset, lineNum, err = readLogDelta(path, 0, 0)
		if err != nil {
			t.Errorf("readLogDelta 返回错误: %v", err)
		}
	})
	if !strings.Contains(out, "first line") || !strings.Contains(out, "[LINE 2] 无法解析: 无法解析的一行") {
		t.Errorf("全量读取输出 = %q", out)
	}
	if lineNum != 2 {
		t.Errorf("lineNum = %d, want 2", lineNum)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 日志文件失败: %v", err)
	}
	if offset != info.Size() {
		t.Errorf("offset = %d, want %d", offset, info.Size())
	}

	// 追加新行后增量读取：只输出新增行，不重复历史行
	appendLogLine(t, path, "2025/12/07 14:04:36 [WARN][log] second line\n")
	prevOffset := offset
	out = captureStdout(t, func() {
		var err error
		offset, lineNum, err = readLogDelta(path, offset, lineNum)
		if err != nil {
			t.Errorf("readLogDelta 返回错误: %v", err)
		}
	})
	if !strings.Contains(out, "second line") {
		t.Errorf("增量读取应输出新行, got %q", out)
	}
	if strings.Contains(out, "first line") {
		t.Errorf("增量读取不应重复输出旧行, got %q", out)
	}
	if lineNum != 3 {
		t.Errorf("lineNum = %d, want 3", lineNum)
	}
	if offset <= prevOffset {
		t.Errorf("offset = %d, want > %d", offset, prevOffset)
	}

	// offset 为负：不执行 Seek，退化为从头读取
	withConfig(t, Config{MinLevel: "DEBUG", NoColor: false})
	out = captureStdout(t, func() {
		if _, _, err := readLogDelta(path, -1, 0); err != nil {
			t.Errorf("readLogDelta 返回错误: %v", err)
		}
	})
	if !strings.Contains(out, "first line") || !strings.Contains(out, Yellow) {
		t.Errorf("offset=-1 应从头读取并带颜色, got %q", out)
	}

	// 文件不存在
	if _, _, err := readLogDelta(filepath.Join(t.TempDir(), "missing.log"), 0, 0); err == nil {
		t.Error("读取不存在的文件应返回错误")
	}

	// 超长行触发 scanner.Err()
	if _, _, err := readLogDelta(writeHugeLineLog(t), 0, 0); err == nil {
		t.Error("超长行应返回错误")
	}
}

// TestReadLogDeltaSkipsBlankLines 覆盖增量读取中“空行只累计行号、不输出”的分支
func TestReadLogDeltaSkipsBlankLines(t *testing.T) {
	path := writeTempLog(t,
		"2025/12/07 14:04:35 [INFO][log] before blank",
		"",
		"2025/12/07 14:04:36 [WARN][log] after blank",
	)
	withConfig(t, Config{MinLevel: "DEBUG", NoColor: true})

	var (
		offset  int64
		lineNum int
	)
	out := captureStdout(t, func() {
		var err error
		offset, lineNum, err = readLogDelta(path, 0, 0)
		if err != nil {
			t.Errorf("readLogDelta 返回错误: %v", err)
		}
	})
	if lineNum != 3 {
		t.Errorf("lineNum = %d, want 3（空行也计入行号）", lineNum)
	}
	if !strings.Contains(out, "before blank") || !strings.Contains(out, "after blank") {
		t.Errorf("输出 = %q, want 包含空行前后两行", out)
	}
	if strings.Contains(out, "[LINE 2]") {
		t.Errorf("空行不应产生输出, got %q", out)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 日志文件失败: %v", err)
	}
	if offset != info.Size() {
		t.Errorf("offset = %d, want %d", offset, info.Size())
	}
}

// ---------- 输出与监控器状态 ----------

func TestClearScreenAndDisplayUsage(t *testing.T) {
	out := captureStdout(t, clearScreen)
	if !strings.Contains(out, "\033[2J") {
		t.Errorf("clearScreen 输出 = %q, want 包含清屏转义", out)
	}

	out = captureStdout(t, displayUsage)
	for _, want := range []string{"用法:", "-level", "-no-color", "-d", "-h, --help"} {
		if !strings.Contains(out, want) {
			t.Errorf("displayUsage 输出缺少 %q: %q", want, out)
		}
	}
}

func TestFileWatcherInitState(t *testing.T) {
	path := writeTempLog(t, "line1", "line2", "line3")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat 日志文件失败: %v", err)
	}

	fw := newFileWatcher(path, Config{MinLevel: "DEBUG"})
	fw.initState()
	if fw.lastSize != info.Size() || fw.lastOffset != info.Size() {
		t.Errorf("lastSize/lastOffset = %d/%d, want %d", fw.lastSize, fw.lastOffset, info.Size())
	}
	if fw.lastLine != 3 {
		t.Errorf("lastLine = %d, want 3", fw.lastLine)
	}
	if fw.lastModTime.IsZero() {
		t.Error("lastModTime 应被记录")
	}

	// 文件不存在时保持零值，不panic
	missing := newFileWatcher(filepath.Join(t.TempDir(), "missing.log"), Config{})
	missing.initState()
	if missing.lastSize != 0 || missing.lastLine != 0 || missing.lastOffset != 0 {
		t.Errorf("文件缺失时状态 = %d/%d/%d, want 全 0", missing.lastSize, missing.lastLine, missing.lastOffset)
	}
}

// ---------- main()：子进程驱动 ----------

// TestLogreaderCLIChild 只在子进程中执行，用真实的 os.Args 调用 main()，验证 CLI 行为与退出码
func TestLogreaderCLIChild(t *testing.T) {
	scenario := os.Getenv(childEnv)
	if scenario == "" {
		t.Skip("由 TestLogreaderCLI 以子进程方式驱动")
	}

	switch scenario {
	case "no-args":
		os.Args = []string{"logreader"}
	case "missing-file":
		os.Args = []string{"logreader", os.Getenv(childFileEnv)}
	case "help":
		os.Args = []string{"logreader", "-h"}
	case "read-file":
		os.Args = []string{"logreader", "-no-color", os.Getenv(childFileEnv)}
		main() // 正常读取后返回
		return
	case "watch-mode":
		os.Args = []string{"logreader", "-no-color", "-d", os.Getenv(childFileEnv)}
		done := make(chan struct{})
		go func() {
			main()
			close(done)
		}()
		time.Sleep(pollWait)
		select {
		case <-done:
			t.Fatal("main() 在 -d 监控模式下不应返回")
		default:
		}
		os.Exit(0)
	default:
		t.Fatalf("未知场景: %s", scenario)
	}

	main()
	t.Fatalf("场景 %s 应通过 os.Exit 结束进程", scenario)
}

func TestLogreaderCLI(t *testing.T) {
	if os.Getenv(childEnv) != "" {
		t.Skip("子进程模式不运行父测试")
	}

	logPath := writeTempLog(t,
		"2025/12/07 14:04:35 [INFO][log] log inited",
		"无法解析的一行",
	)
	missing := filepath.Join(t.TempDir(), "missing.log")

	t.Run("无参数", func(t *testing.T) {
		stdout, stderr, code := runChildAsLogreader(t, childCLITest, "no-args", "")
		if code != 1 {
			t.Errorf("退出码 = %d, want 1", code)
		}
		if !strings.Contains(stderr, "请提供日志文件路径") {
			t.Errorf("stderr = %q, want 提示缺少文件路径", stderr)
		}
		if !strings.Contains(stdout, "用法:") {
			t.Errorf("stdout = %q, want 打印用法", stdout)
		}
	})

	t.Run("文件不存在", func(t *testing.T) {
		_, stderr, code := runChildAsLogreader(t, childCLITest, "missing-file", missing)
		if code != 1 {
			t.Errorf("退出码 = %d, want 1", code)
		}
		if !strings.Contains(stderr, "无法打开文件") {
			t.Errorf("stderr = %q, want 报错无法打开文件", stderr)
		}
	})

	t.Run("读取文件", func(t *testing.T) {
		stdout, stderr, code := runChildAsLogreader(t, childCLITest, "read-file", logPath)
		if code != 0 {
			t.Errorf("退出码 = %d, want 0, stderr = %q", code, stderr)
		}
		if !strings.Contains(stdout, "[INFO] [log] log inited") {
			t.Errorf("stdout = %q, want 输出日志行", stdout)
		}
		if !strings.Contains(stdout, "[LINE 2] 无法解析: 无法解析的一行") {
			t.Errorf("stdout = %q, want 提示无法解析的行", stdout)
		}
	})

	t.Run("帮助", func(t *testing.T) {
		_, stderr, code := runChildAsLogreader(t, childCLITest, "help", "")
		if code != 0 {
			t.Errorf("退出码 = %d, want 0", code)
		}
		if !strings.Contains(stderr, "-level") || !strings.Contains(stderr, "-d") {
			t.Errorf("stderr = %q, want 打印选项说明", stderr)
		}
	})

	t.Run("监控模式", func(t *testing.T) {
		stdout, stderr, code := runChildAsLogreader(t, childCLITest, "watch-mode", logPath)
		if code != 0 {
			t.Errorf("退出码 = %d, want 0, stderr = %q", code, stderr)
		}
		if !strings.Contains(stdout, "log inited") {
			t.Errorf("stdout = %q, want -d 模式先全量输出已有日志", stdout)
		}
	})
}

// TestMainHelpBranch 覆盖 main 中 -h/--help 的提前返回分支。
// 真实 CLI 下 flag.CommandLine 是 ExitOnError，-h 在 flag.Parse 内部就已退出进程；
// 这里换成 ContinueOnError 的 FlagSet，从而走到 main 自身的分支。
func TestMainHelpBranch(t *testing.T) {
	oldArgs, oldFlagSet, oldConfig := os.Args, flag.CommandLine, config
	flag.CommandLine = flag.NewFlagSet("logreader-test", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	os.Args = []string{"logreader", "-h"}
	t.Cleanup(func() {
		os.Args, flag.CommandLine, config = oldArgs, oldFlagSet, oldConfig
	})

	out := captureStdout(t, main)
	if !strings.Contains(out, "用法:") {
		t.Errorf("main 输出 = %q, want 打印用法后返回", out)
	}
}

// ---------- watch()：子进程驱动 ----------

// TestLogreaderWatchChild 只在子进程中执行，覆盖 watch() 的“有新内容 / 文件被截断 / stat 失败”分支
func TestLogreaderWatchChild(t *testing.T) {
	if os.Getenv(childEnv) != "watch-branches" {
		t.Skip("由 TestLogreaderCLIWatchBranches 以子进程方式驱动")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "watch.log")
	if err := os.WriteFile(path, []byte("2025/12/07 14:04:35 [INFO][log] first line\n"), 0644); err != nil {
		t.Fatalf("写入监控日志失败: %v", err)
	}

	// 监控 goroutine 会持续读取包级 config，这里直接赋值而不用 withConfig：
	// 测试结束时还原会让“测试 goroutine 写 / 监控 goroutine 读”构成数据竞争。
	config = Config{MinLevel: "DEBUG", NoColor: true}

	fw := newFileWatcher(path, config)
	fw.initState()
	go fw.watch()

	// 新增内容：走增量读取分支
	appendLogLine(t, path, "2025/12/07 14:04:36 [WARN][log] second line\n")
	time.Sleep(pollWait)

	// 文件变小：走截断分支（清屏 + 全量重读）
	if err := os.WriteFile(path, []byte("2025/12/07 14:04:37 [ERROR][log] after truncate\n"), 0644); err != nil {
		t.Fatalf("截断日志失败: %v", err)
	}
	time.Sleep(pollWait)

	// 文件消失：stat 失败后继续轮询而不是退出
	if err := os.Remove(path); err != nil {
		t.Fatalf("删除日志失败: %v", err)
	}
	time.Sleep(pollWait)

	// 必须用 os.Exit 结束子进程：只有经 os.Exit 退出时，本进程的覆盖率计数器才会刷写到
	// GOCOVERDIR 并被子进程驱动方合并；若让测试正常返回，testing 的 teardown 会先处理
	// 覆盖率输出，本次运行 watch() 的分支覆盖就收集不到。
	_ = os.RemoveAll(dir)
	os.Exit(0)
}

func TestLogreaderCLIWatchBranches(t *testing.T) {
	if os.Getenv(childEnv) != "" {
		t.Skip("子进程模式不运行父测试")
	}

	stdout, stderr, code := runChildAsLogreader(t, childWatchTest, "watch-branches", "")
	if code != 0 {
		t.Errorf("退出码 = %d, want 0, stderr = %q", code, stderr)
	}
	for _, want := range []string{"second line", "after truncate", "\033[2J"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout 缺少 %q: %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "first line") {
		t.Errorf("增量读取与截断后重读都不应重复历史行, got %q", stdout)
	}
	if !strings.Contains(stderr, "错误") {
		t.Errorf("stderr = %q, want 文件被删除后的 stat 错误", stderr)
	}
}
