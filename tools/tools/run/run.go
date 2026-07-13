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
	"strings"
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
		Description: "A Enum decided which type of task want to do. Must Be First Parameter. Enum: [\"shell\"]",
	},
	"reason": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "A short(<=20 words) reason of this task. Must Be Second Parameter",
	},
	"command": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: `Command or program will be run. Must Be Third Parameter`,
	},
	"timeout": {
		Type:        parser.ToolTypeNumber,
		Required:    false,
		Description: "Timeout of the command. Default is 60(seconds). If it will not be run in background(default), it must less than 300(seconds)",
	},
	"sandbox": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "Whether run in sandbox. Some type don't support this parameter. Default is true",
	},
	// "background": {
	// 	Type:        parser.ToolTypeBoolen,
	// 	Required:    false,
	// 	Description: "Whether run in background. Default is false",
	// },
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
	session.ToolCallingContext[toolCallID] = respObj
	session.ToolCallingType[toolCallID] = "run"

	return true, cross, nil
}

// errResult 快速构造错误响应（减少重复的 boolx/success/error 构造模式）
func errResult(msg string, cross []*any) (bool, []*any, map[string]*any, error) {
	f := false
	s := any(f)
	e := any(msg)
	return false, cross, map[string]*any{"success": &s, "error": &e}, nil
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
	if runType != "shell" {
		return errResult(fmt.Sprintf("[System] Parameter Error: type '%s' not supported, only 'shell' is allowed", runType), cross)
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

	timeoutObj, ok := mp["timeout"]
	var timeout int32
	if !ok || timeoutObj == nil {
		timeout = 60
	} else {
		if v, ok := asInt32(timeoutObj); ok {
			timeout = v
		} else {
			timeout = 60
		}
	}
	if timeout <= 0 {
		timeout = 60
	}
	if timeout >= 300 {
		return errResult("[System] Parameter Error: timeout must less than 300", cross)
	}

	logger.Info("run shell \"%s\"(reason: %s)(%ds) sandbox:%v in ID=%d,agentID=%s", command, reason, timeout, sandboxFlag, session.ID, session.CurrentAgentID)

	// get shell
	shell := getShell(config.GlobalConfig.Agent.UseShell)

	// start task
	isolateMode := sandbox.IsolationNone
	if sandboxFlag {
		isolateMode = sandbox.IsolationOS
	}
	env := os.Environ()
	env = append(env, "SANDBOX=alkaid0")
	env = append(env, "TERM=xterm-256color")
	sand, err := sandbox.New(sandbox.Config{
		WorkDir:       path.Join(session.Root, session.CurrentActivatePath),
		Env:           env,
		Timeout:       time.Duration(timeout)*time.Second + 1*time.Second,
		IsolationMode: isolateMode,
	})
	if err != nil {
		return false, cross, nil, err
	}
	startCmd := []string{}
	switch shell {
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		startCmd = []string{"-Command", command}
	case "cmd", "cmd.exe":
		startCmd = []string{"/C", command}
	default:
		startCmd = []string{"-c", command}
	}
	c, err := sand.Execute(shell, startCmd...)
	if err != nil {
		return false, cross, nil, err
	}

	// 注册停止回调，使 loop.Stop() 能直接 kill 此进程
	session.SetToolKillFn(func() { c.Kill() })
	defer session.SetToolKillFn(nil)

	var buf bytes.Buffer

	// 监听context的Done信号，当context被取消时强制kill进程
	ctx := session.GetContext()

	// 使用 PTY 运行命令（Unix），若不可用则回退到缓冲区模式（Windows）
	err = runCmd(c, &buf, ctx, command)

	errString := ""
	if err != nil {
		if sandboxFlag && !sandboxSpecified && strings.Contains(err.Error(), "unshare") {
			errString = "[System] Sandbox unavailable, fallback to non-sandbox\n"
			sand2, err2 := sandbox.New(sandbox.Config{
				WorkDir:       path.Join(session.Root, session.CurrentActivatePath),
				Env:           env,
				Timeout:       time.Duration(timeout)*time.Second + 1*time.Second,
				IsolationMode: sandbox.IsolationNone,
			})
			if err2 != nil {
				errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err)
				outStr := errString + buf.String()
				boolx := false
				success := any(boolx)
				outAny := any(outStr)
				return false, cross, map[string]*any{
					"success": &success,
					"error":   &outAny,
				}, nil
			}
			c2, err2 := sand2.Execute(shell, startCmd...)
			if err2 != nil {
				errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err2)
				outStr := errString + buf.String()
				boolx := false
				success := any(boolx)
				outAny := any(outStr)
				return false, cross, map[string]*any{
					"success": &success,
					"error":   &outAny,
				}, nil
			}
			var buf2 bytes.Buffer

			// 覆盖为 c2 的停止回调（fallback 使用新进程）
			session.SetToolKillFn(func() { c2.Kill() })

			// 监听context的Done信号
			ctx2 := session.GetContext()

			err2 = runCmd(c2, &buf2, ctx2, command)

			if err2 != nil {
				errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err2)
			}
			outStr := errString + buf2.String()
			boolx := err2 == nil
			success := any(boolx)
			outAny := any(outStr)
			res := map[string]*any{
				"success": &success,
				"path":    &outAny,
			}
			if !boolx {
				res["error"] = &outAny
			}
			return false, cross, res, nil
		}
		errString = fmt.Sprintf("[System] Command Execute Error: %v\n", err)
	}

	idAny, ok := mp["_id"]
	toolID := ""
	if !ok || idAny == nil {
		toolID = "unknown"
	} else {
		toolID, ok = (*idAny).(string)
		if !ok {
			toolID = "unknown"
		}
	}

	outStr := errString + buf.String()
	// gettime
	timeStr := time.Now().Format("20060102-150405")
	path := "run/" + toolID + "-" + timeStr
	trace.AddTempObject(session, path, outStr, true)
	logger.Info("command execution finished, output saved to: %s", path)
	outPth := "@temp/" + path
	outAny := any(outPth)
	reasonAny := any(reason)
	boolx := err == nil
	success := any(boolx)
	res := map[string]*any{
		"success": &success,
		"reason":  &reasonAny,
		"path":    &outAny,
	}
	if !boolx {
		res["error"] = &outAny
	}
	return false, cross, res, nil

}

// runCmd 执行命令，优先使用 PTY，否则回退到缓冲区模式。
// runCmd 内部处理 context 取消监听和输出收集。
func runCmd(c *sandbox.Command, buf *bytes.Buffer, ctx context.Context, command string) error {
	// 监听 context 取消信号
	contextDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, killing command: %s", command)
			if err := c.Kill(); err != nil {
				logger.Warn("Failed to kill command: %v", err)
			}
		case <-contextDone:
		}
	}()
	defer close(contextDone)

	master, slave, ptyErr := openPTYForCmd()
	if ptyErr == nil {
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
		copyWg.Add(1)
		go func() {
			defer copyWg.Done()
			_, _ = io.Copy(buf, master)
		}()

		// 等待命令完成
		err := c.Wait()

		// 关闭主端，io.Copy 收到 EOF 后退出
		_ = master.Close()
		copyWg.Wait()
		return err
	}

	// 非 PTY 模式（Windows/fallback）：使用缓冲区直接收集输出
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
