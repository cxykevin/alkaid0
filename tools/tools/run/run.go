package run

import (
	_ "embed" // embed
	"fmt"
	"path"
	"runtime"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/library/json"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/terminal/pty"
	"github.com/cxykevin/alkaid0/terminal/sandbox"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
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
		Description: "A Enum decided which type of task want to do. Must Be First Parameter",
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
	TypeOutputed       bool
	CommandOutputedLen int32
	ReasonOutputedLen  int32
	SandboxOutputed    bool
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, error) {
	tmp, ok := session.TemporyDataOfRequest["tools:run"]
	if !ok || tmp == nil {
		session.TemporyDataOfRequest["tools:run"] = toolCallFlagTempory{}
		tmp = session.TemporyDataOfRequest["tools:run"]
	}
	tmpObj := tmp.(toolCallFlagTempory)
	if pathPtr, ok := mp["path"]; ok && pathPtr != nil {
		if path, ok := (*pathPtr).(string); ok {
			if !tmpObj.TypeOutputed {
				fmt.Printf("Run type: %s\n", path)
				tmpObj.TypeOutputed = true
			}
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
			fmt.Print("Run reason: ")
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
		return false, cross, nil, fmt.Errorf("type is required")
	}
	runType, ok := (*runTypeObj).(string)
	if !ok {
		return false, cross, nil, fmt.Errorf("type must be string")
	}
	if runType != "shell" {
		return false, cross, nil, fmt.Errorf("type not found")
	}

	reasonObj, ok := mp["reason"]
	if !ok || reasonObj == nil {
		return false, cross, nil, fmt.Errorf("reason is required")
	}
	reason, ok := (*reasonObj).(string)
	if !ok {
		return false, cross, nil, fmt.Errorf("reason must be string")
	}
	if reason == "" {
		return false, cross, nil, fmt.Errorf("reason is empty")
	}

	cmdObj, ok := mp["command"]
	if !ok || cmdObj == nil {
		return false, cross, nil, fmt.Errorf("command is required")
	}
	command, ok := (*cmdObj).(string)
	if !ok {
		return false, cross, nil, fmt.Errorf("command must be string")
	}
	if command == "" {
		return false, cross, nil, fmt.Errorf("command is empty")
	}

	var sandboxFlag bool
	sandboxObj, ok := mp["sandbox"]
	if !ok || sandboxObj == nil {
		sandboxFlag = true
	} else {
		sandboxFlag, ok = (*sandboxObj).(bool)
		if !ok {
			sandboxFlag = true
		}
	}

	timeoutObj, ok := mp["timeout"]
	var timeout int32
	if !ok || timeoutObj == nil {
		timeout = 60
	} else {
		timeout, ok = (*timeoutObj).(int32)
		if !ok {
			timeout = 60
		}
	}
	if timeout >= 300 {
		return false, cross, nil, fmt.Errorf("timeout must less than 300")
	}

	logger.Info("run bash \"%s\"(reason: %s)(%ds) sandbox:%v in ID=%d,agentID=%s", command, reason, timeout, sandboxFlag, session.ID, session.CurrentAgentID)

	// TODO: background command

	rows := 80
	cols := 24

	// start pty
	ptyObj, fs, err := pty.New(pty.Config{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		return false, cross, nil, err
	}
	defer ptyObj.Close()
	defer fs.Close()

	// start term buffer
	// buf := buffer.New(rows, cols)
	// buf := bytes.NewBuffer([]byte{})

	// start pty task
	// ctx, cancel := context.WithCancel(context.Background())
	// defer cancel()
	// reader := NewAsyncPipeReader(ctx, ptyObj.File(), 64)
	// defer reader.Close()
	// go func() {
	// 	err := reader.CopyTo(ctx, func(data []byte) error {
	// 		_, err := buf.Write(data)
	// 		return err
	// 	})
	// 	if err != nil {
	// 		logger.Error("run bash error in copy: %v", err)
	// 	}
	// }()

	// get shell
	shell := getShell(config.GlobalConfig.Agent.UseShell)

	// start task
	isolateMode := sandbox.IsolationNone
	if sandboxFlag {
		isolateMode = sandbox.IsolationOS
	}
	sand, err := sandbox.New(sandbox.Config{
		WorkDir: path.Join(session.Root, session.CurrentActivatePath),
		Env: []string{
			"SANDBOX=alkaid0",
		},
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
	cmd, err := sand.Execute(shell, startCmd...)
	if err != nil {
		return false, cross, nil, err
	}

	cmd.SetStdin(nil)
	cmd.SetStdout(fs)
	cmd.SetStderr(fs)
	err = cmd.Run()
	errString := ""
	if err != nil {
		errString = fmt.Sprintf("[System] Command Execute Error: %v\n", err)
	}
	// get output
	// output := buf.String()
	output := ""
	bytes := make([]byte, 1024)
	n, _ := ptyObj.File().Read(bytes)
	output = string(bytes[:n])
	outStr := errString + output
	outAny := any(outStr)
	return false, cross, map[string]*any{
		"output": &outAny,
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
