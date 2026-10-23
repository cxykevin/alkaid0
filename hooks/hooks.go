// Package hooks 执行配置中定义的事件钩子命令。
//
// 钩子命令在对应事件发生时同步执行（阻塞到命令退出），用于把会话的关键时刻
// 接到外部脚本上：会话 ID 与工作目录通过 SESSION_ID / SESSION_PATH 环境变量
// 传给命令，命令的工作目录即会话工作目录。
//
// shell 与 run 工具一致：取 Agent.UseShell，未配置时用平台默认
// （linux=bash、darwin=zsh、windows=powershell.exe），见 internal/shellutil。
package hooks

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/internal/shellutil"
	"github.com/cxykevin/alkaid0/log"
)

var logger = log.New("hooks")

// Event 钩子事件名（与 config.HooksConfig 的字段一一对应）。
type Event string

const (
	// EventCronjobWakeup cron 定时任务唤醒会话
	EventCronjobWakeup Event = "OnCronjobWakeup"
	// EventUserMessage 用户发送普通消息（非斜杠命令）
	EventUserMessage Event = "OnUserMessage"
	// EventTaskFinish 一轮 AI 回合结束
	EventTaskFinish Event = "OnTaskFinish"
)

// Run 同步执行 event 对应的全部命令，返回前会等待每条命令退出（阻塞）。
//
// sessionID 与 sessionPath 分别以 SESSION_ID / SESSION_PATH 环境变量传给命令；
// sessionPath 同时作为命令的工作目录（目录不存在时沿用当前进程目录）。
// 单条命令失败只记录日志，不会中断后续命令，也不影响调用方的会话流程。
func Run(event Event, sessionID uint32, sessionPath string) {
	cmds := commands(event)
	if len(cmds) == 0 {
		return
	}
	id := strconv.FormatUint(uint64(sessionID), 10)
	for i, cmdline := range cmds {
		cmdline = strings.TrimSpace(cmdline)
		if cmdline == "" {
			continue
		}
		if err := runOne(event, i+1, cmdline, id, sessionPath); err != nil {
			logger.Warn("hook %s [%d] %q failed: %v", event, i+1, cmdline, err)
			continue
		}
		logger.Debug("hook %s [%d] %q done", event, i+1, cmdline)
	}
}

// commands 返回当前配置里 event 对应的命令列表。
// 每次调用都读取最新的 config.GlobalConfig，因此 /reload 后立即生效。
func commands(event Event) []string {
	cfg := config.GlobalConfig.Hooks
	switch event {
	case EventCronjobWakeup:
		return cfg.OnCronjobWakeup
	case EventUserMessage:
		return cfg.OnUserMessage
	case EventTaskFinish:
		return cfg.OnTaskFinish
	default:
		return nil
	}
}

// runOne 用配置的 shell 执行一条命令，等待其退出。
//
// shell 与 run 工具（AI 的命令执行）完全一致：Agent.UseShell 未配置时按平台默认，
// 参数约定也相同（powershell -NoProfile -Command / cmd /C / 其它 -c）。
func runOne(event Event, index int, cmdline, sessionID, sessionPath string) error {
	shell := shellutil.Default(config.GlobalConfig.Agent.UseShell)
	cmd := exec.Command(shell, shellutil.Args(shell, cmdline)...)
	if sessionPath != "" {
		if st, err := os.Stat(sessionPath); err == nil && st.IsDir() {
			cmd.Dir = sessionPath
		}
	}
	// 环境变量：进程环境 + 配置的 Agent.TerminalEnvs（与终端任务一致），
	// 但不像 run 工具那样注入 SANDBOX 等终端专用变量。
	env := os.Environ()
	for k, v := range config.GlobalConfig.Agent.TerminalEnvs {
		env = append(env, k+"="+v)
	}
	// 会话信息最后写入：exec 对重复键保留最后一个，避免被 TerminalEnvs 覆盖。
	env = append(env,
		"SESSION_ID="+sessionID,
		"SESSION_PATH="+sessionPath,
	)
	cmd.Env = env
	out := &commandOutput{event: event, index: index}
	cmd.Stdout = out
	cmd.Stderr = out
	defer out.flush()
	return cmd.Run()
}

// commandOutput 把钩子命令的输出按行转发到日志。
// stdout 与 stderr 由 exec 的不同 goroutine 写入，需要加锁。
type commandOutput struct {
	event Event
	index int

	mu  sync.Mutex
	buf strings.Builder
}

func (w *commandOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	for {
		s := w.buf.String()
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(s[:i], "\r")
		w.buf.Reset()
		w.buf.WriteString(s[i+1:])
		w.logLine(line)
	}
	return len(p), nil
}

// flush 输出缓冲区里没有换行结尾的残留内容。
func (w *commandOutput) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	line := strings.TrimRight(w.buf.String(), "\r\n")
	w.buf.Reset()
	w.logLine(line)
}

func (w *commandOutput) logLine(line string) {
	if line == "" {
		return
	}
	logger.Info("hook %s [%d] %s", w.event, w.index, line)
}
