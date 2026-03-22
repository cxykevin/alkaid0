package request

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	libjson "github.com/cxykevin/alkaid0/library/json"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
	"github.com/cxykevin/alkaid0/provider/request/build"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/provider/response"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools"
	"github.com/cxykevin/alkaid0/ui/state"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// UserAddMsg 用户发送消息
func UserAddMsg(session *storageStructs.Chats, msg string, refers *storageStructs.MessagesReferList) error {
	logger.Info("UserAddMsg: chatID=%d, msgLen=%d", session.ID, len(msg))
	db := session.DB
	chatID := session.ID
	var refer storageStructs.MessagesReferList
	if refers == nil {
		refer = storageStructs.MessagesReferList{}
	} else {
		refer = *refers
	}

	if session.CurrentAgentID != "" {
		err := actions.DeactivateAgent(session, "<| user stopped subagent |>")
		if err != nil {
			return err
		}
	}

	if session.State == state.StateWaitApprove {
		reason := prompts.Render(prompts.UserRejectTemplate, msg)
		if err := db.Create(&storageStructs.Messages{
			ChatID: chatID,
			Delta:  reason,
			Refers: refer,
			Type:   storageStructs.MessagesRoleCommunicate,
		}).Error; err != nil {
			return err
		}
		session.State = state.StateIdle
		return db.Save(session).Error
	}

	// 插入
	err := db.Create(&storageStructs.Messages{
		ChatID: chatID,
		Delta:  msg,
		Refers: refer,
		Type:   storageStructs.MessagesRoleUser,
	}).Error
	if err != nil {
		return err
	}
	return nil
}

func stringDefault(str *string) string {
	if str == nil {
		return ""
	}
	return *str
}

// toolCallExprEnv 定义了自动审批/拒绝规则表达式的执行环境。
// 规则可以通过访问 ToolCalls（所有调用）、ToolCall（当前调用）和 Agent 配置来做出决策。
type toolCallExprEnv struct {
	ToolCalls []ToolCall
	ToolCall  ToolCall
	Agent     cfgStructs.AgentConfig
}

// mergeAutoRuleExpr 将用户定义的规则与系统内置规则合并。
// 使用逻辑或 (||) 连接，意味着只要任一规则触发（审批或拒绝），该决策即生效。
func mergeAutoRuleExpr(userExpr string, builtinExpr string) string {
	userExpr = strings.TrimSpace(userExpr)
	builtinExpr = strings.TrimSpace(builtinExpr)
	if userExpr == "" {
		return builtinExpr
	}
	if builtinExpr == "" {
		return userExpr
	}
	return "(" + userExpr + ") || (" + builtinExpr + ")"
}

func hasParam(call ToolCall, key string) bool {
	if call.Parameters == nil {
		return false
	}
	_, ok := call.Parameters[key]
	return ok
}

func param(call ToolCall, key string) any {
	if call.Parameters == nil {
		return nil
	}
	value, ok := call.Parameters[key]
	if !ok || value == nil {
		return nil
	}
	return *value
}

func exprTruthy(value any) bool {
	if value == nil {
		return false
	}
	if value == true {
		return true
	}
	if value == false {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.String:
		return v.String() != ""
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return v.Float() != 0
	case reflect.Slice, reflect.Array, reflect.Map:
		return v.Len() > 0
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return false
		}
		return exprTruthy(v.Elem().Interface())
	default:
		return true
	}
}

// ToolCall 工具调用
type ToolCall struct {
	Name       string          `json:"name"`
	ID         string          `json:"id"`
	Parameters map[string]*any `json:"parameters"`
}

func (t ToolCall) AsMap() map[string]any {
	return map[string]any{
		"Name":       t.Name,
		"ID":         t.ID,
		"Parameters": t.Parameters,
	}
}

// compileExpr 编译表达式字符串为可执行程序，并注入自定义函数（如 regex, contains）。
// 这允许安全规则利用强大的字符串处理能力来识别危险参数。
func compileExpr(program string) (*vm.Program, error) {
	return expr.Compile(program, expr.Env(map[string]any{
		"ToolCalls": []map[string]any{},
		"ToolCall":  map[string]any{},
		"Agent":     cfgStructs.AgentConfig{},
	}), expr.Function("regex", func(params ...any) (any, error) {
		if len(params) != 2 {
			return false, nil
		}
		pattern, ok := params[0].(string)
		if !ok {
			return false, nil
		}
		text, ok := params[1].(string)
		if !ok {
			return false, nil
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, err
		}
		return re.MatchString(text), nil
	}), expr.Function("contains", func(params ...any) (any, error) {
		if len(params) != 2 {
			return false, nil
		}
		s, ok := params[0].(string)
		if !ok {
			return false, nil
		}
		sub, ok := params[1].(string)
		if !ok {
			return false, nil
		}
		return strings.Contains(s, sub), nil
	}), expr.Function("hasParam", func(params ...any) (any, error) {
		if len(params) != 2 {
			return false, nil
		}
		var call ToolCall
		if m, ok := params[0].(map[string]any); ok {
			if name, ok := m["Name"].(string); ok {
				call.Name = name
			}
			if id, ok := m["ID"].(string); ok {
				call.ID = id
			}
			if params, ok := m["Parameters"].(map[string]*any); ok {
				call.Parameters = params
			}
		} else if c, ok := params[0].(ToolCall); ok {
			call = c
		} else {
			return false, nil
		}
		key, ok := params[1].(string)
		if !ok {
			return false, nil
		}
		return hasParam(call, key), nil
	}), expr.Function("param", func(params ...any) (any, error) {
		if len(params) != 2 {
			return nil, nil
		}
		var call ToolCall
		if m, ok := params[0].(map[string]any); ok {
			if name, ok := m["Name"].(string); ok {
				call.Name = name
			}
			if id, ok := m["ID"].(string); ok {
				call.ID = id
			}
			if params, ok := m["Parameters"].(map[string]*any); ok {
				call.Parameters = params
			}
		} else if c, ok := params[0].(ToolCall); ok {
			call = c
		} else {
			return nil, nil
		}
		key, ok := params[1].(string)
		if !ok {
			return nil, nil
		}
		return param(call, key), nil
	}))
}

// CanAutoApprove 根据配置的表达式规则判断一组工具调用是否可以自动执行。
// 逻辑顺序：先检查拒绝规则（任一调用触发拒绝则整体不自动执行），再检查审批规则（所有调用必须触发审批）。
// 这种设计确保了安全性优先：只要有一个调用被认为可疑，就必须人工介入。
func CanAutoApprove(session *storageStructs.Chats, toolCalls []ToolCall, msg *storageStructs.Messages) (bool, string, error) {
	if session == nil || msg == nil || len(toolCalls) == 0 {
		return false, "", nil
	}

	autoApprove := strings.TrimSpace(session.CurrentAgentConfig.AutoApprove)
	autoReject := strings.TrimSpace(session.CurrentAgentConfig.AutoReject)
	// 优先级：Agent 级别配置 > 全局默认配置
	if autoApprove == "" {
		autoApprove = strings.TrimSpace(config.GlobalConfig.Agent.DefaultAutoApprove)
	}
	if autoReject == "" {
		autoReject = strings.TrimSpace(config.GlobalConfig.Agent.DefaultAutoReject)
	}

	builtinAutoApprove := ""
	builtinAutoReject := ""
	if !config.GlobalConfig.Agent.IgnoreDefaultRules {
		builtinAutoApprove = strings.TrimSpace(builtinAutoApproveExpr)
		builtinAutoReject = strings.TrimSpace(builtinAutoRejectExpr)
	}

	autoApprove = mergeAutoRuleExpr(autoApprove, builtinAutoApprove)
	autoReject = mergeAutoRuleExpr(autoReject, builtinAutoReject)

	logger.Debug("autoApprove expr: %s", autoApprove)
	logger.Debug("autoReject expr: %s", autoReject)

	var approveProgram *vm.Program
	var rejectProgram *vm.Program
	var err error
	if autoReject != "" {
		rejectProgram, err = compileExpr(autoReject)
		if err != nil {
			logger.Error("compile autoReject expr error: %v", err)
			return false, "", err
		}
	}
	if autoApprove != "" {
		approveProgram, err = compileExpr(autoApprove)
		if err != nil {
			logger.Error("compile autoApprove expr error: %v", err)
			return false, "", err
		}
	}

	callsMap := make([]map[string]any, len(toolCalls))
	for i, c := range toolCalls {
		callsMap[i] = c.AsMap()
	}

	// 1. 拒绝检查：只要有一个工具调用命中了拒绝规则，则整体不自动执行
	if rejectProgram != nil {
		for _, call := range toolCalls {
			result, runErr := expr.Run(rejectProgram, map[string]any{
				"ToolCalls": callsMap,
				"ToolCall":  call.AsMap(),
				"Agent":     session.CurrentAgentConfig,
			})
			if runErr != nil {
				logger.Error("run autoReject expr error: %v", runErr)
				return false, "", runErr
			}
			if exprTruthy(result) {
				logger.Info("autoReject matched for tool: %s", call.Name)
				return false, "", nil
			}
		}
	}

	// 2. 审批检查：如果没有配置审批规则，默认不自动执行
	if approveProgram == nil {
		return false, "", nil
	}

	// 3. 审批检查：所有工具调用都必须命中审批规则，才允许自动执行
	for _, call := range toolCalls {
		result, runErr := expr.Run(approveProgram, map[string]any{
			"ToolCalls": callsMap,
			"ToolCall":  call.AsMap(),
			"Agent":     session.CurrentAgentConfig,
		})
		if runErr != nil {
			logger.Error("run autoApprove expr error: %v", runErr)
			return false, "", runErr
		}
		if !exprTruthy(result) {
			logger.Info("autoApprove NOT matched for tool: %s", call.Name)
			return false, "", nil
		}
	}

	logger.Info("all tool calls auto-approved")
	return true, "", nil
}

// ParseToolsFromJSON 解析工具调用
func ParseToolsFromJSON(payload string) ([]ToolCall, error) {
	if payload == "" {
		return nil, nil
	}
	parser := libjson.New()
	if err := parser.AddToken(payload); err != nil {
		return nil, err
	}
	if err := parser.DoneToken(); err != nil {
		return nil, err
	}
	if parser.FullCallingObject == nil {
		return nil, errors.New("invalid tools json: empty")
	}

	root := *parser.FullCallingObject
	var arrayItems []*any
	switch typed := root.(type) {
	case []*any:
		arrayItems = typed
	case libjson.ArraySlot:
		arrayItems = []*any(typed)
	default:
		return nil, errors.New("invalid tools json: expected array")
	}

	tools := make([]ToolCall, 0, len(arrayItems))
	for _, item := range arrayItems {
		if item == nil {
			tools = append(tools, ToolCall{})
			continue
		}
		obj, ok := (*item).(map[string]*any)
		if !ok {
			if slot, okSlot := (*item).(libjson.ObjectSlot); okSlot {
				obj = map[string]*any(slot)
			} else {
				return nil, errors.New("invalid tools json: tool object")
			}
		}

		var tool ToolCall
		if namePtr, ok := obj["name"]; ok && namePtr != nil {
			if name, okName := (*namePtr).(string); okName {
				tool.Name = name
			}
		}
		if idPtr, ok := obj["id"]; ok && idPtr != nil {
			if id, okID := (*idPtr).(string); okID {
				tool.ID = id
			}
		}
		if paramsPtr, ok := obj["parameters"]; ok && paramsPtr != nil {
			switch params := (*paramsPtr).(type) {
			case map[string]*any:
				tool.Parameters = params
			case libjson.ObjectSlot:
				tool.Parameters = map[string]*any(params)
			}
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// RejectToolCallsNoDeactivate 自动拒绝工具调用（不退出 subagent）
func RejectToolCallsNoDeactivate(session *storageStructs.Chats, reason string, refers *storageStructs.MessagesReferList) error {
	if session.State != state.StateWaitApprove {
		return nil
	}
	if session.DB == nil {
		return errors.New("db not initialized")
	}
	refer := storageStructs.MessagesReferList{}
	if refers != nil {
		refer = *refers
	}
	finalReason := prompts.Render(prompts.UserRejectTemplate, reason)
	if err := session.DB.Create(&storageStructs.Messages{
		ChatID: session.ID,
		Delta:  finalReason,
		Refers: refer,
		Type:   storageStructs.MessagesRoleCommunicate,
	}).Error; err != nil {
		return err
	}
	session.State = state.StateIdle
	return session.DB.Save(session).Error
}

// ApplyToolOnHooks 应用工具调用
func ApplyToolOnHooks(session *storageStructs.Chats, toolCallingJSON string) error {
	if toolCallingJSON == "" {
		return nil
	}
	toolCalls, err := ParseToolsFromJSON(toolCallingJSON)
	if err != nil {
		return err
	}
	for _, call := range toolCalls {
		if err := tools.ExecToolOnHook(session, call.Name, call.Parameters); err != nil {
			return err
		}
	}
	return nil
}

// ExecuteToolCalls 执行工具调用
func ExecuteToolCalls(session *storageStructs.Chats, toolCallingJSON string) (bool, error) {
	if toolCallingJSON == "" {
		return true, nil
	}
	if session.DB == nil {
		return true, errors.New("db not initialized")
	}
	session.State = state.StateToolCalling
	if err := session.DB.Save(session).Error; err != nil {
		return true, err
	}
	if err := ApplyToolOnHooks(session, toolCallingJSON); err != nil {
		session.State = state.StateIdle
		if saveErr := session.DB.Save(session).Error; saveErr != nil {
			return true, saveErr
		}
		return true, err
	}

	solver := response.NewSolver(session.DB, session)
	_, _, err := solver.AddToken("<tools>"+toolCallingJSON+"</tools>", "")
	if err != nil {
		session.State = state.StateIdle
		if saveErr := session.DB.Save(session).Error; saveErr != nil {
			return true, saveErr
		}
		return true, err
	}
	ok, _, _, err := solver.DoneToken()
	session.State = state.StateIdle
	if saveErr := session.DB.Save(session).Error; saveErr != nil {
		return ok, saveErr
	}
	return ok, err
}

// SendRequest 发送请求
func SendRequest(ctx context.Context, session *storageStructs.Chats, callback func(string, string) error) (bool, error) {
	session.State = state.StateWaiting
	session.TemporyDataOfRequest = make(map[string]any)
	db := session.DB

	modelID := session.LastModelID
	if session.CurrentAgentID != "" {
		modelIDRet := uint32(session.CurrentAgentConfig.AgentModel)
		if modelIDRet != 0 {
			modelID = modelIDRet
		}
	}
	// 取模型ID
	// var chat structs.Chats
	// err := db.First(&chat, chatID).Error
	// if err != nil {
	// 	return true, err
	// }
	modelCfg, ok := config.GlobalConfig.Model.Models[int32(modelID)]
	logger.Info("SendRequest: using model %s (ID: %d)", modelCfg.ModelName, modelID)
	if !ok {
		return true, errors.New("model not found")
	}

	// var agentConfig *cfgStruct.AgentConfig = nil
	// if agentID != "" {
	// 	agentConfig, err = getAgentConfig(agentID)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// }

	solver := response.NewSolver(db, session)
	agent := session.CurrentAgentID
	// 写库
	reqObj := storageStructs.Messages{
		ChatID:        session.ID,
		AgentID:       &agent,
		Delta:         "",
		ThinkingDelta: "",
		Type:          storageStructs.MessagesRoleAgent,
		ModelID:       modelID,
		ModelName:     modelCfg.ModelName,
	}
	tx := db.Create(&reqObj)
	// 取主键
	if tx.Error != nil {
		return true, tx.Error
	}

	var gDelta strings.Builder
	var gThinkingDelta strings.Builder
	var pendingDelta strings.Builder
	var pendingThinkingDelta strings.Builder
	var lastFlushLen int
	var lastFlushThinkingLen int
	msgID := reqObj.ID
	// tokenFlushThreshold 定义了向数据库刷新消息内容的阈值。
	// 在流式响应中，如果每收到一个 token 就写入数据库，会对磁盘 I/O 造成巨大压力。
	// 通过累积一定数量的 token（此处为 256）再统一更新，可以显著提升性能，同时保证用户在刷新页面时能看到大部分内容。
	const tokenFlushThreshold = 256
	solveFunc := func(body reqStruct.ChatCompletionResponse) error {
		if session.State == state.StateRequesting {
			session.State = state.StateReciving
		}
		if len(body.Choices) == 0 {
			return nil
		}
		// 调用 solver 解析 token（可能包含 <think> 或 <tools> 标签）
		delta, thinkingDelta, err := solver.AddToken(body.Choices[0].Delta.Content, stringDefault(body.Choices[0].Delta.ReasoningContent))
		gDelta.WriteString(delta)
		gThinkingDelta.WriteString(thinkingDelta)
		pendingDelta.WriteString(delta)
		pendingThinkingDelta.WriteString(thinkingDelta)
		if err != nil {
			return err
		}
		// 达到阈值时执行数据库更新
		shouldFlush := pendingDelta.Len()+pendingThinkingDelta.Len() >= tokenFlushThreshold
		if shouldFlush {
			gstring := gDelta.String()
			gtstring := gThinkingDelta.String()
			if err := db.Model(&storageStructs.Messages{}).Where("id = ?", msgID).Updates(storageStructs.Messages{
				Delta:         gstring,
				ThinkingDelta: gtstring,
			}).Error; err != nil {
				return err
			}
			pendingDelta.Reset()
			pendingThinkingDelta.Reset()
			lastFlushLen = len(gstring)
			lastFlushThinkingLen = len(gtstring)
		}
		// 回调函数通常用于实时推送到 UI 界面
		if err := callback(delta, thinkingDelta); err != nil {
			return err
		}
		return nil
	}

	session.State = state.StateGeneratingPrompt
	logger.Debug("SendRequest: generating prompt for chat %d", session.ID)
	obj, err := build.Build(db, session)
	if err != nil {
		return true, err
	}

	// 留日志
	// 生成json
	var buf bytes.Buffer
	encoder := stdjson.NewEncoder(&buf)
	encoder.SetIndent("", "    ")
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(obj)
	if err == nil {
		logger.Debug("[request body] %s", buf.String())
	}

	session.State = state.StateRequesting

	err = SimpleOpenAIRequest(ctx, modelCfg.ProviderURL, modelCfg.ProviderKey, modelCfg.ModelID, *obj, solveFunc)
	if err != nil {
		return true, err
	}
	ok, delta, thinkingDelta, err := solver.DoneToken()
	if err != nil {
		return true, err
	}
	gDelta.WriteString(delta)
	tools := solver.GetTools()
	gThinkingDelta.WriteString(thinkingDelta)
	if gDelta.String() == "" && gThinkingDelta.String() == "" && len(tools) == 0 {
		// 删除
		err = db.Delete(&storageStructs.Messages{}, msgID).Error
	} else {
		finalDelta := gDelta.String()
		finalThinkingDelta := gThinkingDelta.String()
		if len(finalDelta) != lastFlushLen || len(finalThinkingDelta) != lastFlushThinkingLen {
			err = db.Model(&storageStructs.Messages{}).Where("id = ?", msgID).Updates(storageStructs.Messages{
				Delta:         finalDelta,
				ThinkingDelta: finalThinkingDelta,
			}).Error
		}
		if err == nil {
			err = db.Model(&storageStructs.Messages{}).Where("id = ?", msgID).Update(
				"tool_calling_json_string", string(solver.GetToolsOrigin()),
			).Error
		}
	}
	if err != nil {
		return true, err
	}
	if len(tools) > 0 {
		session.State = state.StateWaitApprove
		if saveErr := db.Save(session).Error; saveErr != nil {
			return true, saveErr
		}
		return true, nil
	}
	err = callback(delta, thinkingDelta)
	if err != nil {
		return true, err
	}

	logger.Debug("[tool body] %s", solver.GetToolsOrigin())
	return ok, nil
}
