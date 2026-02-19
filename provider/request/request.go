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

type toolCallExprEnv struct {
	ToolCalls []ToolCall
	ToolCall  ToolCall
	Agent     cfgStructs.AgentConfig
}

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

func compileExpr(program string) (*vm.Program, error) {
	return expr.Compile(program, expr.Env(toolCallExprEnv{}), expr.Function("regex", func(params ...any) (any, error) {
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
		call, ok := params[0].(ToolCall)
		if !ok {
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
		call, ok := params[0].(ToolCall)
		if !ok {
			return nil, nil
		}
		key, ok := params[1].(string)
		if !ok {
			return nil, nil
		}
		return param(call, key), nil
	}))
}

// CanAutoApprove 自动审批策略（待补充）
func CanAutoApprove(session *storageStructs.Chats, toolCalls []ToolCall, msg *storageStructs.Messages) (bool, string, error) {
	if session == nil || msg == nil || len(toolCalls) == 0 {
		return false, "", nil
	}

	autoApprove := strings.TrimSpace(session.CurrentAgentConfig.AutoApprove)
	autoReject := strings.TrimSpace(session.CurrentAgentConfig.AutoReject)
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

	var approveProgram *vm.Program
	var rejectProgram *vm.Program
	var err error
	if autoReject != "" {
		rejectProgram, err = compileExpr(autoReject)
		if err != nil {
			return false, "", err
		}
	}
	if autoApprove != "" {
		approveProgram, err = compileExpr(autoApprove)
		if err != nil {
			return false, "", err
		}
	}

	if rejectProgram != nil {
		for _, call := range toolCalls {
			result, runErr := expr.Run(rejectProgram, toolCallExprEnv{
				ToolCalls: toolCalls,
				ToolCall:  call,
				Agent:     session.CurrentAgentConfig,
			})
			if runErr != nil {
				return false, "", runErr
			}
			if exprTruthy(result) {
				return false, "", nil
			}
		}
	}

	if approveProgram == nil {
		return false, "", nil
	}

	for _, call := range toolCalls {
		result, runErr := expr.Run(approveProgram, toolCallExprEnv{
			ToolCalls: toolCalls,
			ToolCall:  call,
			Agent:     session.CurrentAgentConfig,
		})
		if runErr != nil {
			return false, "", runErr
		}
		if !exprTruthy(result) {
			return false, "", nil
		}
	}

	return true, "", nil
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

// ToolCall 工具调用
type ToolCall struct {
	Name       string          `json:"name"`
	ID         string          `json:"id"`
	Parameters map[string]*any `json:"parameters"`
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
	const tokenFlushThreshold = 256
	solveFunc := func(body reqStruct.ChatCompletionResponse) error {
		if session.State == state.StateRequesting {
			session.State = state.StateReciving
		}
		if len(body.Choices) == 0 {
			return nil
		}
		delta, thinkingDelta, err := solver.AddToken(body.Choices[0].Delta.Content, stringDefault(body.Choices[0].Delta.ReasoningContent))
		gDelta.WriteString(delta)
		gThinkingDelta.WriteString(thinkingDelta)
		pendingDelta.WriteString(delta)
		pendingThinkingDelta.WriteString(thinkingDelta)
		if err != nil {
			return err
		}
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
		if err := callback(delta, thinkingDelta); err != nil {
			return err
		}
		return nil
	}

	session.State = state.StateGeneratingPrompt
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
