package run

import (
	"bytes"
	_ "embed" // embed
	"fmt"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
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
	"github.com/shirou/gopsutil/v4/host"
)

const toolName = "run"

var sysVer string = ""

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
		Type:        parser.ToolTypeInt,
		Required:    false,
		Description: "Timeout of the command. Default is 60(seconds). If it will not be run in background(default), it must less than 300(seconds)",
	},
	"sandbox": {
		Type:        parser.ToolTypeBoolen,
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

type toolCallFlagTempory struct {
	TypeOutputedLen    int32
	CommandOutputedLen int32
	ReasonOutputedLen  int32
	SandboxOutputed    bool
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

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, error) {
	tmp, ok := session.TemporyDataOfRequest["tools:run"]
	if !ok || tmp == nil {
		session.TemporyDataOfRequest["tools:run"] = toolCallFlagTempory{}
		tmp = session.TemporyDataOfRequest["tools:run"]
	}
	tmpObj := tmp.(toolCallFlagTempory)

	if typePtr, ok := mp["type"]; ok && typePtr != nil {
		var typeOut string
		if text, ok := (*typePtr).(string); ok {
			typeOut = text
		}
		if text, ok := (*typePtr).(json.StringSlot); ok {
			typeOut = string(text)
		}
		if typeOut != "" && int(tmpObj.TypeOutputedLen) == 0 {
			fmt.Print("Run type: ")
		}
		if typeOut != "" && int(tmpObj.TypeOutputedLen) < len(typeOut) {
			fmt.Print(typeOut[tmpObj.TypeOutputedLen:])
			tmpObj.TypeOutputedLen = int32(len(typeOut))
		}
	}
	if reasonPtr, ok := mp["reason"]; ok && reasonPtr != nil {
		var reasonOut string
		if text, ok := (*reasonPtr).(string); ok {
			reasonOut = text
		}
		if text, ok := (*reasonPtr).(json.StringSlot); ok {
			reasonOut = string(text)
		}
		if reasonOut != "" && int(tmpObj.ReasonOutputedLen) == 0 {
			fmt.Print("\nRun reason: ")
		}
		if reasonOut != "" && int(tmpObj.ReasonOutputedLen) < len(reasonOut) {
			fmt.Print(reasonOut[tmpObj.ReasonOutputedLen:])
			tmpObj.ReasonOutputedLen = int32(len(reasonOut))
		}
	}
	if cmdPtr, ok := mp["command"]; ok && cmdPtr != nil {
		var cmdOut string
		if text, ok := (*cmdPtr).(string); ok {
			cmdOut = text
		}
		if text, ok := (*cmdPtr).(json.StringSlot); ok {
			cmdOut = string(text)
		}
		if cmdOut != "" && int(tmpObj.CommandOutputedLen) == 0 {
			fmt.Print("\nRun command: ")
		}
		if cmdOut != "" && int(tmpObj.CommandOutputedLen) < len(cmdOut) {
			fmt.Print(cmdOut[tmpObj.CommandOutputedLen:])
			tmpObj.CommandOutputedLen = int32(len(cmdOut))
		}
	}
	if sandPtr, ok := mp["sandbox"]; ok && sandPtr != nil {
		if sandbox, ok := (*sandPtr).(bool); ok {
			if !tmpObj.SandboxOutputed {
				fmt.Printf("\nRun in sandbox: %v\n", sandbox)
				tmpObj.SandboxOutputed = true
			}
		}
	}
	session.TemporyDataOfRequest["tools:run"] = tmpObj
	return true, cross, nil
}

func runTask(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	runTypeObj, ok := mp["type"]
	if !ok || runTypeObj == nil {
		out := any("[System] Parameter Error: type is required")
		return false, cross, map[string]*any{"output": &out}, nil
	}
	runType, ok := asString(runTypeObj)
	if !ok {
		out := any("[System] Parameter Error: type must be string")
		return false, cross, map[string]*any{"output": &out}, nil
	}
	if runType != "shell" {
		out := any(fmt.Sprintf("[System] Parameter Error: type '%s' not supported, only 'shell' is allowed", runType))
		return false, cross, map[string]*any{"output": &out}, nil
	}

	reasonObj, ok := mp["reason"]
	if !ok || reasonObj == nil {
		out := any("[System] Parameter Error: reason is required")
		return false, cross, map[string]*any{"output": &out}, nil
	}
	reason, ok := asString(reasonObj)
	if !ok {
		out := any("[System] Parameter Error: reason must be string")
		return false, cross, map[string]*any{"output": &out}, nil
	}
	if reason == "" {
		out := any("[System] Parameter Error: reason is empty")
		return false, cross, map[string]*any{"output": &out}, nil
	}

	cmdObj, ok := mp["command"]
	if !ok || cmdObj == nil {
		out := any("[System] Parameter Error: command is required")
		return false, cross, map[string]*any{"output": &out}, nil
	}
	command, ok := asString(cmdObj)
	if !ok {
		out := any("[System] Parameter Error: command must be string")
		return false, cross, map[string]*any{"output": &out}, nil
	}
	if command == "" {
		out := any("[System] Parameter Error: command is empty")
		return false, cross, map[string]*any{"output": &out}, nil
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
		out := any("[System] Parameter Error: timeout must less than 300")
		return false, cross, map[string]*any{"output": &out}, nil
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

	var buf bytes.Buffer
	c.SetStdin(nil)
	c.SetStdout(&buf)
	c.SetStderr(&buf)
	err = c.Run()

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
				outAny := any(outStr)
				return false, cross, map[string]*any{
					"output": &outAny,
				}, nil
			}
			c2, err2 := sand2.Execute(shell, startCmd...)
			if err2 != nil {
				errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err2)
				outStr := errString + buf.String()
				outAny := any(outStr)
				return false, cross, map[string]*any{
					"output": &outAny,
				}, nil
			}
			var buf2 bytes.Buffer
			c2.SetStdin(nil)
			c2.SetStdout(&buf2)
			c2.SetStderr(&buf2)
			err2 = c2.Run()
			if err2 != nil {
				errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err2)
			}
			outStr := errString + buf2.String()
			outAny := any(outStr)
			return false, cross, map[string]*any{
				"output": &outAny,
			}, nil
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
	outPth := "@temp/" + path
	outAny := any(outPth)
	reasonAny := any(reason)
	return false, cross, map[string]*any{
		// "output": &outAny,
		"reason": &reasonAny,
		"path":   &outAny,
	}, nil

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
	if sysVer == "" {
		sysVerTmp, err := host.Info()
		if err != nil {
			sysVer = "unknown"
		}
		sysVer = sysVerTmp.Platform + " " + sysVerTmp.PlatformVersion
	}
	return prompts.Render(templateSys, struct {
		Workdir string
		SysOS   string
		Shell   string
		Arch    string
	}{
		Workdir: session.Root + session.CurrentActivatePath,
		SysOS:   runtime.GOOS + "(" + sysVer + ")",
		Shell:   getShell(config.GlobalConfig.Agent.UseShell),
		Arch:    runtime.GOARCH,
	}), nil
}

func load() string {
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            toolName,
		UserDescription: prompt,
		Parameters:      paras,
		ID:              toolName,
	})
	actions.HookTool(toolName, &toolobj.Hook{
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
	})
	actions.HookTool("", &toolobj.Hook{
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
	})
	return toolName
}

func init() {
	index.AddIndex(load)
}
