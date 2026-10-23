package structs

// HooksConfig 事件钩子配置。
//
// 每个事件是一组命令：事件发生时按顺序逐条执行，并**阻塞**等待命令退出后
// 才继续会话流程。命令经配置 `Agent.UseShell` 指定的 shell 执行，未配置时
// 与 run 工具使用同一套平台默认（linux=bash、darwin=zsh、windows=powershell.exe），
// 也使用相同的参数约定（powershell -NoProfile -Command / cmd /C / 其它 -c）。
// 命令的工作目录为会话的工作目录；环境变量为进程环境 + 配置的 Agent.TerminalEnvs
// （与终端任务一致，但不注入 SANDBOX 等 run 工具专用变量），并注入：
//
//	SESSION_ID   会话 ID（数字）
//	SESSION_PATH 会话工作目录（绝对路径）
//
// 命令失败（非零退出码/无法启动）只记录日志，不影响会话本身。
// 留空或为 null 表示该事件没有钩子。
type HooksConfig struct {
	// OnCronjobWakeup cron 定时任务唤醒会话时执行
	OnCronjobWakeup []string
	// OnUserMessage 用户发送普通消息（非斜杠命令）时执行
	OnUserMessage []string
	// OnTaskFinish 一轮 AI 回合结束时执行（含正常结束、用户取消与出错）
	OnTaskFinish []string
}
