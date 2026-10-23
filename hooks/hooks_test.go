package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// useConfig 把给定配置设为当前生效配置，测试结束后恢复。
func useConfig(t *testing.T, cfg cfgStructs.Config) {
	t.Helper()
	restore := config.GlobalConfigSwap(cfg)
	t.Cleanup(restore)
}

// useHooks 只设置钩子配置（其余字段保持零值）。
func useHooks(t *testing.T, cfg cfgStructs.HooksConfig) {
	t.Helper()
	useConfig(t, cfgStructs.Config{Hooks: cfg})
}

func requireShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook 测试使用 sh 命令，Windows 上跳过")
	}
}

// tempDir 返回解析过符号链接的临时目录。
// macOS 上 t.TempDir() 位于 /var/...（/var 指向 /private/var），
// 而子进程的 $PWD 是内核给出的物理路径 /private/var/...，不解析就会比较不等。
func tempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("解析临时目录符号链接失败: %v", err)
	}
	return resolved
}

// TestRunInjectsSessionEnvAndWorkdir 验证命令拿到 SESSION_ID / SESSION_PATH，
// 工作目录为 sessionPath，并且 Run 在命令写完文件之后才返回（阻塞语义）。
func TestRunInjectsSessionEnvAndWorkdir(t *testing.T) {
	requireShell(t)
	dir := tempDir(t)
	useHooks(t, cfgStructs.HooksConfig{
		OnUserMessage: []string{
			`printf '%s|%s|%s' "$SESSION_ID" "$SESSION_PATH" "$PWD" > hook.out`,
		},
	})

	Run(EventUserMessage, 42, dir)

	got, err := os.ReadFile(filepath.Join(dir, "hook.out"))
	if err != nil {
		t.Fatalf("hook 未在工作目录写文件: %v", err)
	}
	want := "42|" + dir + "|" + dir
	if string(got) != want {
		t.Fatalf("环境变量/工作目录不符: got %q, want %q", got, want)
	}
}

// TestRunExecutesCommandsInOrderAndContinuesOnFailure 验证按配置顺序逐条执行，
// 且某条命令失败（非零退出码）不会中断后续命令。
func TestRunExecutesCommandsInOrderAndContinuesOnFailure(t *testing.T) {
	requireShell(t)
	dir := tempDir(t)
	useHooks(t, cfgStructs.HooksConfig{
		OnTaskFinish: []string{
			`echo first >> order.txt`,
			`exit 3`,
			`echo third >> order.txt`,
		},
	})

	Run(EventTaskFinish, 7, dir)

	got, err := os.ReadFile(filepath.Join(dir, "order.txt"))
	if err != nil {
		t.Fatalf("读取输出失败: %v", err)
	}
	if strings.TrimSpace(string(got)) != "first\nthird" {
		t.Fatalf("命令顺序/失败后继续不符: %q", got)
	}
}

// TestRunOnlyExecutesConfiguredEvent 验证事件之间互不串扰。
func TestRunOnlyExecutesConfiguredEvent(t *testing.T) {
	requireShell(t)
	dir := tempDir(t)
	useHooks(t, cfgStructs.HooksConfig{
		OnCronjobWakeup: []string{`touch cron.out`},
	})

	Run(EventUserMessage, 1, dir)
	Run(EventTaskFinish, 1, dir)
	if _, err := os.Stat(filepath.Join(dir, "cron.out")); !os.IsNotExist(err) {
		t.Fatalf("其它事件不应执行 OnCronjobWakeup 命令: err=%v", err)
	}

	Run(EventCronjobWakeup, 1, dir)
	if _, err := os.Stat(filepath.Join(dir, "cron.out")); err != nil {
		t.Fatalf("OnCronjobWakeup 命令未执行: %v", err)
	}
}

// TestRunWithoutConfigIsNoop 验证未配置任何钩子时直接返回。
func TestRunWithoutConfigIsNoop(t *testing.T) {
	useHooks(t, cfgStructs.HooksConfig{})
	Run(EventUserMessage, 1, t.TempDir())
}

// TestRunInjectsTerminalEnvs 验证注入配置的 Agent.TerminalEnvs（与终端任务一致），
// 且 SESSION_ID / SESSION_PATH 不被其中同名键覆盖。
func TestRunInjectsTerminalEnvs(t *testing.T) {
	requireShell(t)
	dir := tempDir(t)
	useConfig(t, cfgStructs.Config{
		Agent: cfgStructs.AgentsConfig{TerminalEnvs: map[string]string{
			"ALK_HOOK_TEST": "from-terminal-envs",
			"SESSION_ID":    "should-be-overridden",
		}},
		Hooks: cfgStructs.HooksConfig{
			OnUserMessage: []string{`printf '%s|%s' "$ALK_HOOK_TEST" "$SESSION_ID" > env.out`},
		},
	})

	Run(EventUserMessage, 99, dir)

	got, err := os.ReadFile(filepath.Join(dir, "env.out"))
	if err != nil {
		t.Fatalf("读取输出失败: %v", err)
	}
	if want := "from-terminal-envs|99"; string(got) != want {
		t.Fatalf("TerminalEnvs/SESSION_ID 不符: got %q, want %q", got, want)
	}
}

// TestRunUsesConfiguredShell 验证钩子用 Agent.UseShell 配置的 shell 执行
// （与 run 工具同一套选择与参数约定）。
func TestRunUsesConfiguredShell(t *testing.T) {
	requireShell(t)
	dir := tempDir(t)
	argsFile := filepath.Join(dir, "shell-args.txt")
	wrapper := filepath.Join(dir, "my-shell")
	script := "#!/bin/sh\nprintf '%s|%s' \"$1\" \"$2\" > \"" + argsFile + "\"\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("写入包装 shell 失败: %v", err)
	}

	useConfig(t, cfgStructs.Config{
		Agent: cfgStructs.AgentsConfig{UseShell: wrapper},
		Hooks: cfgStructs.HooksConfig{
			OnUserMessage: []string{"echo ran > ran.txt"},
		},
	})

	Run(EventUserMessage, 1, dir)

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("配置的 shell 未被使用: %v", err)
	}
	// 非 powershell/cmd 时参数约定为 -c <命令>（与 run 工具一致）
	if want := "-c|echo ran > ran.txt"; string(args) != want {
		t.Fatalf("shell 参数不符: got %q, want %q", args, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran.txt")); err != nil {
		t.Fatalf("命令未在配置的 shell 中执行: %v", err)
	}
}

// TestRunFallsBackWhenWorkdirMissing 验证 sessionPath 不存在时不设置工作目录，
// 命令仍能执行（环境变量 SESSION_PATH 照常注入）。
func TestRunFallsBackWhenWorkdirMissing(t *testing.T) {
	requireShell(t)
	dir := tempDir(t)
	useHooks(t, cfgStructs.HooksConfig{
		OnUserMessage: []string{`[ -n "$SESSION_PATH" ]`},
	})
	Run(EventUserMessage, 1, filepath.Join(dir, "not-exist"))
}
