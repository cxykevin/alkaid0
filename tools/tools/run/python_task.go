package run

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/server/apikey"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/terminal/pythonenv"
	"github.com/cxykevin/alkaid0/terminal/sandbox"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
)

var dynworkflowImportPattern = regexp.MustCompile(`(?m)^\s*(?:import\s+dynworkflow(?:\s+as\s+\w+)?(?:\s*$)|from\s+dynworkflow(?:\s+import\s+|\s*$))`)

func containsDynworkflowImport(code string) bool {
	return dynworkflowImportPattern.MatchString(code)
}

// pythonTask 处理 run 工具的 "python" 类型：在全局 venv 中执行 Python 代码。
// - command 参数是完整 Python 源码，通过 Python 的 -c 参数执行（不创建临时脚本）。
// - 每次执行都注入临时 proxy 凭据、地址和模型 ID。
// - 默认使用 sandbox，支持 background 和 timeout。
func pythonTask(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	// 检查 venv 是否就绪
	if !pythonenv.IsReady() {
		initErr := pythonenv.InitError()
		if initErr != nil {
			return errResult(fmt.Sprintf("[System] Python venv initialization failed: %v", initErr), cross)
		}
		return errResult("[System] Python venv is still initializing, please wait and retry", cross)
	}

	reasonObj, ok := mp["reason"]
	if !ok || reasonObj == nil {
		return errResult("[System] Parameter Error: reason is required", cross)
	}
	reason, ok := asString(reasonObj)
	if !ok || reason == "" {
		return errResult("[System] Parameter Error: reason must be non-empty string", cross)
	}

	cmdObj, ok := mp["command"]
	if !ok || cmdObj == nil {
		return errResult("[System] Parameter Error: command is required", cross)
	}
	code, ok := asString(cmdObj)
	if !ok || code == "" {
		return errResult("[System] Parameter Error: command must be non-empty string (Python source code)", cross)
	}

	venvPython := pythonenv.VenvPython()
	if venvPython == "" {
		return errResult("[System] Python venv not initialized", cross)
	}

	modelID, err := resolveCurrentModel(session)
	if err != nil {
		return errResult(fmt.Sprintf("[System] Failed to resolve current model: %v", err), cross)
	}

	// 每次 Python 任务都注入 proxy 凭据，不依赖代码是否导入 openai。
	// 这样动态导入、别名、子进程以及后续代码修改都不会因缺少 OPENAI_API_KEY 失败。
	needsProxy := true

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

	disableSandbox := config.GlobalConfig.Agent.DisableSandbox ||
		session.CurrentAgentConfig.DisableSandbox ||
		os.Getenv("ALKAID0_DISABLE_SANDBOX") == "true"

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
	dynworkflow := containsDynworkflowImport(code)
	if dynworkflow {
		backgroundFlag = true
		logger.Info("forcing dynworkflow Python task into background mode")
	}

	timeoutObj, ok := mp["timeout"]
	var timeout int32
	if !ok || timeoutObj == nil {
		if backgroundFlag {
			timeout = 0
		} else {
			timeout = 60
		}
	} else {
		if v, ok := asInt32(timeoutObj); ok {
			timeout = v
		} else {
			timeout = 60
		}
	}
	if backgroundFlag {
		if timeout < 0 {
			timeout = 0
		}
	} else {
		if timeout >= 300 {
			return errResult("[System] Parameter Error: timeout must less than 300", cross)
		}
		if timeout <= 0 {
			timeout = 60
		}
	}

	idAny, ok := mp["_id"]
	toolID := "unknown"
	if ok && idAny != nil {
		if s, ok := (*idAny).(string); ok {
			toolID = s
		}
	}

	env := os.Environ()
	env = append(env, "SANDBOX=alkaid0")
	env = append(env, "TERM=xterm-256color")
	env = append(env, "PAGER=cat")
	env = append(env, "SYSTEMD_PAGER=cat")
	env = append(env, "GIT_PAGER=cat")
	env = append(env, "DEBIAN_FRONTEND=noninteractive")
	if dynworkflow {
		env = append(env, "ALKAID0_WORKFLOW_REPORT=1")
		env = append(env, fmt.Sprintf("ALKAID0_WORKFLOW_SESSION_ID=%d", session.ID))
	}

	for k, v := range config.GlobalConfig.Agent.TerminalEnvs {
		env = append(env, k+"="+v)
	}

	var apiKey string
	var cleanupFn func()

	if needsProxy {
		key, baseURL, modelID, err := buildProxyEnv(session)
		if err != nil {
			return errResult(fmt.Sprintf("[System] Failed to setup OpenAI proxy: %v", err), cross)
		}
		apiKey = key
		env = mergeEnv(env, []string{
			"OPENAI_API_KEY=" + key,
			"OPENAI_BASE_URL=" + baseURL,
			"OPENAI_MODEL_ID=" + modelID,
		})
		cleanupFn = func() {
			if apikey.Delete(apiKey) {
				logger.Info("deleted temporary API key for python task")
			}
		}
		logger.Info("python task injected proxy env: base=%s model=%s", baseURL, modelID)
	}

	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	displayCmd := fmt.Sprintf("python (execute %d bytes code)", len(code))
	pythonCode := "model = " + strconv.Quote(modelID) + "\n" + code
	// 终端 ID 与 run id 统一为 @temp/run/<n>。ID 的命名空间是**工作目录**（session.Root，
	// 一个目录可有多个会话），因此序号按工作目录重置；进程工作目录仍包含激活路径。
	// 前台命令结束时同样把输出写入该 ID 对应的 temp obj，客户端事后可按 terminal id 取回内容。
	workspace := session.Root
	workDir := path.Join(session.Root, session.CurrentActivatePath)
	runID := NewRunID(workspace)
	// temp obj 内部路径（@temp/run/7 → run/7）
	tempPath, ok := TempPath(runID)
	if !ok {
		if cleanupFn != nil {
			cleanupFn()
		}
		return errResult(fmt.Sprintf("[System] Invalid run id: %s", runID), cross)
	}
	var updateFn func(string)
	if backgroundFlag {
		updateFn = func(content string) {
			_ = trace.UpdateTempObject(session, tempPath, content)
		}
	}

	req := &Request{
		SessionID:        session.ID,
		AgentID:          session.CurrentAgentID,
		ToolID:           toolID,
		Reason:           reason,
		Program:          venvPython,
		Args:             []string{"-c", pythonCode},
		Stdin:            "",
		DisplayCommand:   displayCmd,
		Env:              env,
		WorkDir:          workDir,
		Timeout:          time.Duration(timeout) * time.Second,
		Sandbox:          sandboxFlag,
		SandboxSpecified: sandboxSpecified,
		WritableDirs:     nonEmptyDirs(pythonenv.VenvDir()),
		RunID:            runID,
		Workspace:        workspace,
		UpdateFn:         updateFn,
		CleanupFn:        cleanupFn,
		BackgroundKind: func() string {
			if dynworkflow {
				return "workflow"
			}
			if backgroundFlag {
				return "background"
			}
			return ""
		}(),
		TerminalUpdateFn: func(terminalID, status, content string) { session.PushTerminalUpdate(terminalID, status, content) },
		InteractiveStdin: dynworkflow,
		WorkflowOutputFn: func(runID, visible string, events []WorkflowEvent) {
			if visible != "" {
				// 终端推送与 workflow 事件都使用统一的 run id / terminal id（@temp/run/<n>）。
				session.PushTerminalUpdate(runID, "running", visible)
			}
			for _, event := range events {
				session.PushWorkflowEvent(runID, event)
			}
		},
	}

	if backgroundFlag {
		_ = trace.AddTempObject(session, tempPath, bgInitialContent(displayCmd), true)
		job, err := Default.Submit(context.Background(), req)
		if err != nil {
			if cleanupFn != nil {
				cleanupFn()
			}
			return false, cross, nil, err
		}
		// 工具调用 ACP 携带 run id / terminal id（两者统一为同一个 @temp/run/<n>）。
		session.SetToolCallingRunID(toolCallID, job.ID)
		session.SetToolCallingTerminalID(toolCallID, job.ID)
		logger.Info("run python in background (reason: %s) sandbox:%v openai:%v in ID=%d,agentID=%s runid=%s", reason, sandboxFlag, needsProxy, session.ID, session.CurrentAgentID, job.ID)
		boolx := true
		success := any(boolx)
		bgAny := any(true)
		reasonAny := any(reason)
		outAny := any(job.ID)
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
		if cleanupFn != nil {
			cleanupFn()
		}
		return false, cross, nil, err
	}

	// 工具调用 ACP 携带 run id / terminal id：终端结束后客户端可据此取回持久化内容。
	session.SetToolCallingRunID(toolCallID, job.ID)
	session.SetToolCallingTerminalID(toolCallID, job.ID)

	session.SetToolKillFn(func() { _ = Default.Kill(workspace, job.ID) })
	defer session.SetToolKillFn(nil)

	logger.Info("run python (reason: %s)(%ds) sandbox:%v openai:%v in ID=%d,agentID=%s job=%s", reason, timeout, sandboxFlag, needsProxy, session.ID, session.CurrentAgentID, job.ID)

	result := job.Wait(ctx)

	if result.CreateErr != nil {
		return false, cross, nil, result.CreateErr
	}

	boolx := result.Success
	success := any(boolx)

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

	outStr := "[agent execute] " + displayCmd + "\n\n" + result.ErrString + result.Output
	// 写入本次任务自己的持久化 temp obj：终端结束后仍可按 terminal id 取回。
	_ = trace.AddTempObject(session, tempPath, outStr, true)
	logger.Info("python execution finished, output saved to: %s", tempPath)
	outPth := runID
	outAny := any(outPth)
	reasonAny := any(reason)
	output := outStr
	if len(output) > maxRunOutputChars {
		output = output[:maxRunOutputChars] + "\n...(truncated, full output at " + outPth + ")"
	}
	outputAny := any(output)
	msg := "The file has been read and injected into the top of the context."
	msgAny := any(msg)

	res := map[string]*any{
		"success": &success,
		"reason":  &reasonAny,
		"path":    &outAny,
		"output":  &outputAny,
		"message": &msgAny,
	}
	if !boolx {
		errAny := any(output)
		res["error"] = &errAny
	}
	return false, cross, res, nil
}
