package run

import (
	"bytes"
	"context"
	_ "embed" // embed
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/library/json"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/terminal/sandbox"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	u "github.com/cxykevin/alkaid0/utils"
	"github.com/shirou/gopsutil/v4/host"
)

const toolName = "run"

// sysVerOnce 惰性初始化系统版本信息（线程安全）
var sysVerOnce = sync.OnceValue(func() string {
	info, err := host.Info()
	if err != nil {
		return "unknown"
	}
	return info.Platform + " " + info.PlatformVersion
})

//go:embed prompt.md
var prompt string

//go:embed prompt_sys.md
var promptSys string

var templateSys = prompts.Load("tools:run:sys", promptSys)

var logger = log.New("tools:run")

var paras = map[string]parser.ToolParameters{
	"type": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "A Enum decided which type of task want to do. Must Be First Parameter. Enum: [\"shell\", \"sleep\", \"wait\"]",
	},
	"reason": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "A short(<=20 words) reason of this task. Must Be Second Parameter",
	},
	"command": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: `Command or program will be run. For "sleep" type, it must be an int number representing seconds to wait. For "wait" type, it must be the run id returned by a background run. Must Be Third Parameter`,
	},
	"timeout": {
		Type:        parser.ToolTypeNumber,
		Required:    false,
		Description: "Timeout of the command. Default is 60(seconds). If it will not be run in background(default), it must less than 300(seconds). If run in background, default is no timeout and no limit. Only avaible in \"shell\" type",
	},
	"sandbox": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "Whether run in sandbox. Some type don't support this parameter. Default is true. Only avaible in \"shell\" type",
	},
	"background": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "Whether run in background. Default is false. If true, the command runs asynchronously: a temp object is created immediately and its path returned as run id, updated every 60 seconds until the command finishes. Only avaible in \"shell\" type",
	},
}

// PassInfo 传递信息
type PassInfo struct {
	From        string
	Description string
	Parameters  map[string]any
}

func asInt32(p *any) (int32, bool) {
	if p == nil {
		return 0, false
	}
	switch v := (*p).(type) {
	case int32:
		return v, true
	case int:
		return int32(v), true
	case int64:
		return int32(v), true
	case float64:
		if v != float64(int64(v)) {
			return 0, false
		}
		return int32(v), true
	case string:
		i, err := strconv.Atoi(v)
		if err != nil {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return 0, false
			}
			return int32(f), true
		}
		return int32(i), true
	case json.StringSlot:
		s := string(v)
		i, err := strconv.Atoi(s)
		if err != nil {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return 0, false
			}
			return int32(f), true
		}
		return int32(i), true
	default:
		return 0, false
	}
}

func asString(p *any) (string, bool) {
	if p == nil {
		return "", false
	}
	switch v := (*p).(type) {
	case string:
		return v, true
	case json.StringSlot:
		return string(v), true
	default:
		return "", false
	}
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	respString := ""
	var typeVal *string
	var reasonVal *string
	var commandVal *string
	var sandboxVal *bool
	if typePtr, ok := mp["type"]; ok && typePtr != nil {
		if typev, ok := (*typePtr).(string); ok {
			respString += "Type: " + typev + "\n"
			typeVal = &typev
		}
	}
	if reasonPtr, ok := mp["reason"]; ok && reasonPtr != nil {
		if reason, ok := (*reasonPtr).(string); ok {
			respString += "Reason: " + reason + "\n"
			reasonVal = &reason
		}
	}
	if commandPtr, ok := mp["command"]; ok && commandPtr != nil {
		if command, ok := (*commandPtr).(string); ok {
			respString += "Command: " + command + "\n"
			commandVal = &command
		} else if secs, ok := asInt32(commandPtr); ok {
			commandStr := strconv.Itoa(int(secs))
			respString += "Command: " + commandStr + "s\n"
			commandVal = &commandStr
		}
	}
	if sandboxPtr, ok := mp["sandbox"]; ok && sandboxPtr != nil {
		if sandbox, ok := (*sandboxPtr).(bool); ok {
			respString += "Sandbox: " + u.Ternary(sandbox, "true", "false") + "\n"
			sandboxVal = &sandbox
		}
	}
	respObj := []u.H{{
		"type": "content",
		"content": u.H{
			"type": "text",
			"text": respString,
		},
	}, {
		"type":      "alk.cxykevin.top/calling_info",
		"name":      toolName,
		"messageID": session.CurrentMessageID,
		"args": u.H{
			"type":    typeVal,
			"reason":  reasonVal,
			"command": commandVal,
			"sandbox": sandboxVal,
		},
	}}
	session.SetToolCalling(toolCallID, respObj, "run")

	return true, cross, nil
}

// errResult 快速构造错误响应（减少重复的 boolx/success/error 构造模式）
func errResult(msg string, cross []*any) (bool, []*any, map[string]*any, error) {
	f := false
	s := any(f)
	e := any(msg)
	return false, cross, map[string]*any{"success": &s, "error": &e}, nil
}

// maxSleepSeconds sleep 类型允许的最大等待秒数，防止 AI 让会话无限等待
const maxSleepSeconds = 3600

// maxRunOutputChars 直接随工具结果返回给 AI 的命令输出最大字符数。
// 超出部分截断并提示完整 @temp 路径。让 AI 无需依赖 trace 注入即可直接看到
// 本次调用的命令输出，避免"结果已返回但 AI 看不到、只能反复重试同一命令"的循环。
const maxRunOutputChars = 2000

// sleepTask 处理 run 工具的 "sleep" 类型：让 agent 等待指定秒数，不执行实际命令。
func sleepTask(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	reasonObj, ok := mp["reason"]
	if !ok || reasonObj == nil {
		return errResult("[System] Parameter Error: reason is required", cross)
	}
	reason, ok := asString(reasonObj)
	if !ok {
		return errResult("[System] Parameter Error: reason must be string", cross)
	}
	if reason == "" {
		return errResult("[System] Parameter Error: reason is empty", cross)
	}

	cmdObj, ok := mp["command"]
	if !ok || cmdObj == nil {
		return errResult("[System] Parameter Error: command is required for type 'sleep'", cross)
	}
	seconds, ok := asInt32(cmdObj)
	if !ok {
		return errResult("[System] Parameter Error: command must be int(seconds) for type 'sleep'", cross)
	}
	if seconds < 0 {
		return errResult("[System] Parameter Error: command must be >= 0 for type 'sleep'", cross)
	}
	if seconds > maxSleepSeconds {
		return errResult(fmt.Sprintf("[System] Parameter Error: command must <= %d seconds for type 'sleep'", maxSleepSeconds), cross)
	}

	logger.Info("sleep %d seconds (reason: %s) in ID=%d,agentID=%s", seconds, reason, session.ID, session.CurrentAgentID)

	// 监听 context 取消与 loop.Stop() 中断，使 sleep 可被终止
	ctx := session.GetContext()
	sleepCtx, sleepCancel := context.WithCancel(ctx)
	defer sleepCancel()
	session.SetToolKillFn(sleepCancel)
	defer session.SetToolKillFn(nil)

	if seconds > 0 {
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		defer timer.Stop()
		select {
		case <-sleepCtx.Done():
			logger.Info("sleep interrupted by context cancel")
			boolx := false
			success := any(boolx)
			errStr := any("[System] sleep interrupted")
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errStr,
			}, nil
		case <-timer.C:
		}
	}

	boolx := true
	success := any(boolx)
	reasonAny := any(reason)
	secondsAny := any(seconds)
	res := map[string]*any{
		"success": &success,
		"reason":  &reasonAny,
		"seconds": &secondsAny,
	}
	return false, cross, res, nil
}

// waitTask 处理 run 工具的 "wait" 类型：阻塞等待指定 runid（后台任务 temp obj 路径）结束。
func waitTask(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	runIDObj, ok := mp["command"]
	if !ok || runIDObj == nil {
		return errResult("[System] Parameter Error: command(run id) is required for type 'wait'", cross)
	}
	runID, ok := asString(runIDObj)
	if !ok || runID == "" {
		return errResult("[System] Parameter Error: command must be string(run id) for type 'wait'", cross)
	}

	job := Default.Find(runID)
	if job == nil {
		return errResult(fmt.Sprintf("[System] Run id not found: %s", runID), cross)
	}

	logger.Info("wait runid %s in ID=%d,agentID=%s", runID, session.ID, session.CurrentAgentID)

	// 阻塞直到后台任务结束（不触发 kill，仅等待）
	ctx := session.GetContext()
	select {
	case <-job.Done():
	case <-ctx.Done():
		boolx := false
		success := any(boolx)
		errStr := any("[System] wait interrupted")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errStr,
		}, nil
	}

	boolx := true
	success := any(boolx)
	outAny := any(runID)
	res := map[string]*any{
		"success": &success,
		"path":    &outAny,
	}
	return false, cross, res, nil
}

func runTask(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	runTypeObj, ok := mp["type"]
	if !ok || runTypeObj == nil {
		return errResult("[System] Parameter Error: type is required", cross)
	}
	runType, ok := asString(runTypeObj)
	if !ok {
		return errResult("[System] Parameter Error: type must be string", cross)
	}
	if runType != "shell" && runType != "sleep" && runType != "wait" {
		return errResult(fmt.Sprintf("[System] Parameter Error: type '%s' not supported, only 'shell' and 'sleep' and 'wait' are allowed", runType), cross)
	}

	if runType == "sleep" {
		return sleepTask(session, mp, cross)
	}
	if runType == "wait" {
		return waitTask(session, mp, cross)
	}

	reasonObj, ok := mp["reason"]
	if !ok || reasonObj == nil {
		return errResult("[System] Parameter Error: reason is required", cross)
	}
	reason, ok := asString(reasonObj)
	if !ok {
		return errResult("[System] Parameter Error: reason must be string", cross)
	}
	if reason == "" {
		return errResult("[System] Parameter Error: reason is empty", cross)
	}

	cmdObj, ok := mp["command"]
	if !ok || cmdObj == nil {
		return errResult("[System] Parameter Error: command is required", cross)
	}
	command, ok := asString(cmdObj)
	if !ok {
		return errResult("[System] Parameter Error: command must be string", cross)
	}
	if command == "" {
		return errResult("[System] Parameter Error: command is empty", cross)
	}

	var sandboxFlag bool
	sandboxObj, ok := mp["sandbox"]
	sandboxSpecified := ok && sandboxObj != nil
	if !ok || sandboxObj == nil {
		sandboxFlag = true
	} else {
		sandboxFlag, ok = (*sandboxObj).(bool)
		if !ok {
			sandboxFlag = true
		}
	}

	// 检查配置和环境变量以禁用沙盒
	disableSandbox := config.GlobalConfig.Agent.DisableSandbox ||
		session.CurrentAgentConfig.DisableSandbox ||
		os.Getenv("ALKAID0_DISABLE_SANDBOX") == "true"

	// 检查环境是否支持沙盒
	if sandboxFlag && !disableSandbox {
		if !sandbox.IsSandboxSupported() {
			disableSandbox = true
			logger.Info("Sandbox not supported in current environment, disabling")
		}
	}

	if disableSandbox {
		logger.Info("sandbox disabled by config or environment")
		sandboxFlag = false
	}

	var backgroundFlag bool
	if bgObj, ok := mp["background"]; ok && bgObj != nil {
		if b, ok := (*bgObj).(bool); ok {
			backgroundFlag = b
		}
	}

	timeoutObj, ok := mp["timeout"]
	var timeout int32
	if !ok || timeoutObj == nil {
		if backgroundFlag {
			// 后台任务默认无超时
			timeout = 0
		} else {
			timeout = 60 // 与工具描述一致：默认 60 秒
		}
	} else {
		if v, ok := asInt32(timeoutObj); ok {
			timeout = v
		} else {
			timeout = 60
		}
	}
	if backgroundFlag {
		// 后台任务不受 300 秒限制，显式负值视为无超时
		if timeout < 0 {
			timeout = 0
		}
	} else {
		if timeout >= 300 {
			return errResult("[System] Parameter Error: timeout must less than 300", cross)
		}
		if timeout <= 0 {
			// 显式传 0/负值视为无效，钳制到默认 60，避免静默变成无超时导致命令无限阻塞
			timeout = 60
		}
	}

	// 工具 ID 用于输出文件命名
	idAny, ok := mp["_id"]
	toolID := "unknown"
	if ok && idAny != nil {
		if s, ok := (*idAny).(string); ok {
			toolID = s
		}
	}

	// get shell
	shell := getShell(config.GlobalConfig.Agent.UseShell)

	// 构建命令环境
	env := os.Environ()
	env = append(env, "SANDBOX=alkaid0")
	env = append(env, "TERM=xterm-256color")
	// 禁止交互式分页器/编辑器，防止命令在 PTY 中因等待输入而永久挂起
	env = append(env, "PAGER=cat")
	env = append(env, "SYSTEMD_PAGER=cat")
	env = append(env, "GIT_PAGER=cat")
	env = append(env, "DEBIAN_FRONTEND=noninteractive")

	// 用户配置的终端环境变量
	for k, v := range config.GlobalConfig.Agent.TerminalEnvs {
		env = append(env, k+"="+v)
	}

	// background 模式：runid = temp obj 路径，作为后台任务的唯一标识
	var runid string
	var updateFn func(string)
	if backgroundFlag {
		runid = "run/" + toolID + "-" + time.Now().Format("20060102-150405")
		updateFn = func(content string) {
			_ = trace.UpdateTempObject(session, runid, content)
		}
	}

	// 运行命令 = 新建后台服务（job）并等待响应
	req := &Request{
		SessionID:        session.ID,
		AgentID:          session.CurrentAgentID,
		ToolID:           toolID,
		Command:          command,
		Reason:           reason,
		Shell:            shell,
		Env:              env,
		WorkDir:          path.Join(session.Root, session.CurrentActivatePath),
		Timeout:          time.Duration(timeout) * time.Second,
		Sandbox:          sandboxFlag,
		SandboxSpecified: sandboxSpecified,
		RunID:            runid,
		UpdateFn:         updateFn,
	}

	if backgroundFlag {
		// 先创建 temp obj 并立即返回其路径作为 runid（命令在后台执行）。
		// 用 StoreTempObject：只写 ReferFiles（read 可读完整输出），不写 Traces，
		// 避免 run 输出注入 <tracedFiles> topBlock 污染上下文顶部。
		_ = trace.StoreTempObject(session, runid, bgInitialContent(command), true)
		if _, err := Default.Submit(context.Background(), req); err != nil {
			return false, cross, nil, err
		}
		logger.Info("run shell in background \"%s\"(reason: %s) sandbox:%v in ID=%d,agentID=%s runid=%s", command, reason, sandboxFlag, session.ID, session.CurrentAgentID, runid)
		boolx := true
		success := any(boolx)
		bgAny := any(true)
		reasonAny := any(reason)
		outAny := any("@temp/" + runid)
		res := map[string]*any{
			"success":    &success,
			"background": &bgAny,
			"reason":     &reasonAny,
			"path":       &outAny,
			"run_id":     &outAny,
		}
		return false, cross, res, nil
	}

	ctx := session.GetContext()
	job, err := Default.Submit(ctx, req)
	if err != nil {
		return false, cross, nil, err
	}

	// 注册停止回调，使 loop.Stop() 能直接 kill 此后台任务
	session.SetToolKillFn(func() { _ = Default.Kill(job.ID) })
	defer session.SetToolKillFn(nil)

	logger.Info("run shell \"%s\"(reason: %s)(%ds) sandbox:%v in ID=%d,agentID=%s job=%s", command, reason, timeout, sandboxFlag, session.ID, session.CurrentAgentID, job.ID)

	// 等待后台服务响应
	result := job.Wait(ctx)

	if result.CreateErr != nil {
		return false, cross, nil, result.CreateErr
	}

	boolx := result.Success
	success := any(boolx)

	// 非沙盒降级路径：输出直接作为 path/output 字段，不写 trace（与原行为一致；
	// output 字段与主路径对齐，保证 AI 在任意路径下都能直接从工具结果看到命令输出）
	if result.Fallback {
		outStr := result.ErrString + result.Output
		outAny := any(outStr)
		res := map[string]*any{
			"success": &success,
			"path":    &outAny,
			"output":  &outAny,
		}
		if !boolx {
			res["error"] = &outAny
		}
		return false, cross, res, nil
	}

	// 主路径：保存到 @temp 文件供 AI 用 read 读完整输出。
	// 用 StoreTempObject：只写 ReferFiles，不写 Traces——run 输出不再进 <tracedFiles>
	// topBlock（此前聚合可达数百 KB、几十条 run 记录，AI 难以定位本次结果）。
	// 本次输出摘要直接随工具结果返回（output 字段），完整内容按 path 可读。
	outStr := "[agent execute] $ " + command + "\n\n" + result.ErrString + result.Output
	timeStr := time.Now().Format("20060102-150405")
	tracePath := "run/" + toolID + "-" + timeStr
	_ = trace.StoreTempObject(session, tracePath, outStr, true)
	logger.Info("command execution finished, output saved to: %s", tracePath)
	outPth := "@temp/" + tracePath
	outAny := any(outPth)
	reasonAny := any(reason)
	// 命令输出直接随工具结果返回（截断到 maxRunOutputChars），AI 在 role:tool 消息里
	// 第一眼就能看到本次调用结果，不必从 trace 聚合块（可能数百 KB、几十条 run 记录）
	// 中大海捞针地定位最新输出。完整内容仍保存在 @temp 路径可读。
	output := outStr
	if len(output) > maxRunOutputChars {
		output = output[:maxRunOutputChars] + "\n...(truncated, full output at " + outPth + ")"
	}
	outputAny := any(output)
	msg := "The file has been traced and injected into the top of the context."
	msgAny := any(msg)
	res := map[string]*any{
		"success": &success,
		"reason":  &reasonAny,
		"path":    &outAny,
		"output":  &outputAny,
		"message": &msgAny,
	}
	if !boolx {
		res["error"] = &outputAny
	}
	return false, cross, res, nil

}

// runCmd 执行命令，优先使用 PTY，否则回退到缓冲区模式。
// runCmd 内部处理 context 取消监听和输出收集。
func runCmd(ctx context.Context, c *sandbox.Command, buf *bytes.Buffer, command string) error {
	master, slave, ptyErr := openPTYForCmd()
	if ptyErr == nil {
		// 先打开 PTY，再启动 context 监听，确保监听 goroutine 能访问 master
		contextDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				logger.Info("Context cancelled, killing command: %s", command)
				if err := c.Kill(); err != nil {
					logger.Warn("Failed to kill command: %v", err)
				}
				// 关闭 master 解除 io.Copy 阻塞（孤儿进程可能仍持有 slave fd）
				_ = master.Close()
			case <-contextDone:
			}
		}()
		defer close(contextDone)

		// PTY 模式：将子进程 stdio 挂载到 PTY 从端
		c.SetStdin(slave)
		c.SetStdout(slave)
		c.SetStderr(slave)

		if err := c.Start(); err != nil {
			_ = master.Close()
			_ = slave.Close()
			return err
		}

		// 关闭从端（子进程已有自己的副本）
		_ = slave.Close()

		// 从主端读取输出到缓冲区
		var copyWg sync.WaitGroup
		copyWg.Go(func() {
			_, _ = io.Copy(buf, master)
		})

		// 等待命令完成
		err := c.Wait()

		// 关闭主端，io.Copy 收到 EOF 后退出
		_ = master.Close()
		copyWg.Wait()
		return err
	}

	// 非 PTY 模式（Windows/fallback）：使用缓冲区直接收集输出
	contextDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, killing command: %s", command)
			_ = c.Kill()
		case <-contextDone:
		}
	}()
	defer close(contextDone)
	c.SetStdin(nil)
	c.SetStdout(buf)
	c.SetStderr(buf)
	return c.Run()
}

func getShell(shell string) string {
	if shell == "" {
		switch runtime.GOOS {
		case "linux":
			return "bash"
		case "darwin":
			return "zsh"
		case "windows":
			return "powershell.exe"
		default:
			return "bash"
		}
	}
	return shell
}

func genOSInfo(session *structs.Chats) (string, error) {
	sysVer := sysVerOnce()
	rendered, err := prompts.Render(templateSys, struct {
		Workdir string
		SysOS   string
		Shell   string
		Arch    string
	}{
		Workdir: session.Root + session.CurrentActivatePath,
		SysOS:   runtime.GOOS + "(" + sysVer + ")",
		Shell:   getShell(config.GlobalConfig.Agent.UseShell),
		Arch:    runtime.GOARCH,
	})
	if err != nil {
		return "", err
	}
	return rendered, nil
}

func load() string {
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            toolName,
		UserDescription: prompt,
		Parameters:      paras,
		ID:              toolName,
	})
	if err := actions.HookTool(toolName, &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 50,
			Func:     runTask,
		},
	}); err != nil {
		panic(err)
	}
	if err := actions.HookTool("", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     genOSInfo,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     nil,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     nil,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
