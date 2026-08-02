package request

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	libjson "github.com/cxykevin/alkaid0/library/json"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/mask"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
	"github.com/cxykevin/alkaid0/provider/request/build"
	"github.com/cxykevin/alkaid0/provider/request/classifier"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/provider/response"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools"
	"github.com/cxykevin/alkaid0/ui/state"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"gorm.io/gorm"
)

// errNativeToolCallFormat 标记模型绕过 <tools> 标签、直接输出原生 tool calling 格式。
// 该错误由 solveFunc 在流式检测到原生格式时返回，SimpleOpenAIRequest 会包装为
// "callback error: ..." 传回，SendRequest 据此判定"打回"（拒绝本次响应并重试）。
var errNativeToolCallFormat = errors.New("native tool calling format detected, reject response")

// 打回注入的格式纠正消息模板。消息以 user 角色插入，模型下一轮请求时
// 会通过 UserWrapTemplate 渲染为 <user_prompt> 看到，从而改用 <tools> 标签。
const nativeFormatCorrectionMsg = `[System: Tool call format rejected]

你上一条回复使用了原生 function-calling 格式（例如 {"tool_calls":[...]} 或 {"name":"...","arguments":"..."}），但本系统不解析该格式，工具不会执行，任务因此失败。

所有工具调用必须使用 <tools> 标签包裹的 JSON 数组，并且必须放在回复的最后：
<tools>
[{"name":"工具名","id":"唯一id","parameters":{...}}]
</tools>

要求：
1. "name" 必须匹配系统提供的工具名；
2. "id" 是任意唯一字符串；
3. "parameters" 必须是真实的 JSON 对象（键值对），绝不能是字符串或转义文本；
4. <tools> 后不能再有任何文字。

请重新输出你的工具调用。`

// injectNativeFormatCorrection 打回时向对话历史注入一条格式纠正消息，
// 使模型在下一轮请求中收到明确反馈并改用 <tools> 标签。
// AgentID 跟随当前会话（子代理场景下子代理按 agent_id 过滤消息，
// 若不设置则子代理读不到纠正反馈，会再次输出原生格式造成重复打回）。
func injectNativeFormatCorrection(db *gorm.DB, session *storageStructs.Chats) error {
	agent := session.CurrentAgentID
	return db.Create(&storageStructs.Messages{
		ChatID:  session.ID,
		Delta:   nativeFormatCorrectionMsg,
		Type:    storageStructs.MessagesRoleUser,
		AgentID: &agent,
		Refers:  storageStructs.MessagesReferList{},
	}).Error
}

// UserAddMsg 处理用户发送的消息，更新数据库并处理子代理和审批状态
// 当 session 处于 WaitApprove 状态时，用户消息会被同时作为拒绝原因（写入
// Communicate 消息）和正常用户消息（写入 MessagesRoleUser）处理，确保输入不丢失。
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

	// 第 1 步：处理 WaitApprove 状态 — 先写入拒绝消息，再继续处理用户输入
	if session.State == state.StateWaitApprove {
		logger.Info("UserAddMsg: state=WaitApprove, rejecting pending tools and processing user input")
		reason, err := prompts.Render(prompts.UserRejectTemplate, msg)
		if err != nil {
			return err
		}
		if err := db.Create(&storageStructs.Messages{
			ChatID: chatID,
			Delta:  reason,
			Refers: refer,
			Type:   storageStructs.MessagesRoleCommunicate,
		}).Error; err != nil {
			return err
		}
		session.State = state.StateIdle
		if err := db.Save(session).Error; err != nil {
			return err
		}
		// NOTE: 不 return — 继续执行后续逻辑，将用户输入作为正常消息插入
	}

	// 第 2 步：停用子代理（如果有）
	if session.CurrentAgentID != "" {
		err := actions.DeactivateAgent(session, "<| user stopped subagent |>")
		if err != nil {
			return err
		}
	}

	// 分类并转换消息（prompt/code/log 三段分类）
	var transformedMsg string
	var segInfos []classifier.SegmentInfo
	if !config.GlobalConfig.Agent.DisablePromptPreprocess {
		var err error
		transformedMsg, segInfos, err = classifier.ClassifyAndTransform(session, msg)
		if err != nil {
			logger.Warn("classifier transform failed (falling back to original): %v", err)
			transformedMsg = msg
		}
	} else {
		transformedMsg = msg
	}

	// 插入
	msgRecord := storageStructs.Messages{
		ChatID: chatID,
		Delta:  transformedMsg,
		Refers: refer,
		Type:   storageStructs.MessagesRoleUser,
	}
	if err := db.Create(&msgRecord).Error; err != nil {
		return err
	}

	// 存储分类标签
	for _, si := range segInfos {
		if err := db.Create(&storageStructs.ClassifySegment{
			ChatID:    chatID,
			MessageID: msgRecord.ID,
			Label:     si.Label,
			Text:      si.Text,
			TempPath:  si.TempPath,
		}).Error; err != nil {
			logger.Warn("failed to store classify segment: %v", err)
		}
	}

	return nil
}

// SubAgentReject 处理子代理被拒绝时的状态回退
func SubAgentReject(session *storageStructs.Chats) error {
	logger.Info("SubAgentReject: chatID=%d", session.ID)
	db := session.DB
	chatID := session.ID
	var refer storageStructs.MessagesReferList

	if session.State == state.StateWaitApprove {
		reason := "<| tool call automatically rejected due to lack of explicit approval |>"
		if err := db.Create(&storageStructs.Messages{
			ChatID:  chatID,
			Delta:   reason,
			Refers:  refer,
			Type:    storageStructs.MessagesRoleCommunicate,
			AgentID: &session.CurrentAgentID,
		}).Error; err != nil {
			return err
		}
		session.State = state.StateIdle
		return db.Save(session).Error
	}
	return nil
}

func stringDefault(str *string) string {
	if str == nil {
		return ""
	}
	return *str
}

// // toolCallExprEnv 定义了自动审批/拒绝规则表达式的执行环境。
// // 规则可以通过访问 ToolCalls（所有调用）、ToolCall（当前调用）和 Agent 配置来做出决策。
// type toolCallExprEnv struct {
// 	ToolCalls []ToolCall
// 	ToolCall  ToolCall
// 	Agent     cfgStructs.AgentConfig
// }

// mergeAutoRuleExpr 将用户定义的规则与系统内置规则合并。
// 使用 truthy() 包装两侧表达式确保类型安全——用户可能写入 "true"（字符串）
// 或其他非布尔类型的表达式，直接与内置规则用 || 连接会导致 expr-lang 类型错误。
// truthy() 函数会在 expr 环境中注册，委托给 Go 的 exprTruthy。
func mergeAutoRuleExpr(userExpr string, builtinExpr string) string {
	userExpr = strings.TrimSpace(userExpr)
	builtinExpr = strings.TrimSpace(builtinExpr)
	if userExpr == "" {
		return builtinExpr
	}
	if builtinExpr == "" {
		return userExpr
	}
	return "truthy(" + userExpr + ") || truthy(" + builtinExpr + ")"
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
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return v != ""
	case int:
		return v != 0
	case int8:
		return v != 0
	case int16:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case uint:
		return v != 0
	case uint8:
		return v != 0
	case uint16:
		return v != 0
	case uint32:
		return v != 0
	case uint64:
		return v != 0
	case float32:
		return v != 0
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case *any:
		if v == nil {
			return false
		}
		return exprTruthy(*v)
	case []*any:
		return len(v) > 0
	case map[string]*any:
		return len(v) > 0
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

// AsMap 将 ToolCall 转换为 map[string]any
func (t ToolCall) AsMap() map[string]any {
	return map[string]any{
		"Name":       t.Name,
		"ID":         t.ID,
		"Parameters": t.Parameters,
	}
}

// ApprovalDecision 审批决策类型，消除旧有 (bool, string) 返回值的三重歧义。
type ApprovalDecision uint8

const (
	// DecisionManual 表示无任何规则匹配，需用户手动审批
	DecisionManual ApprovalDecision = iota
	// DecisionApproved 表示所有工具调用均命中自动审批规则
	DecisionApproved
	// DecisionRejected 表示至少一个工具调用命中了自动拒绝规则
	DecisionRejected
)

// ApprovalResult 是 EvaluateApprovalRules 的结构化返回值。
// Reason 仅在 Decision == DecisionRejected 时有意义。
type ApprovalResult struct {
	Decision ApprovalDecision
	Reason   string
}

// EvaluateApprovalRules 评估配置的自动审批/拒绝规则，返回明确决策。
// 评估顺序（安全优先）：
//  1. 编译用户+内置合并规则
//  2. 先检查拒绝规则——任一匹配即整体拒绝
//  3. 无审批规则时返回 DecisionManual
//  4. 检查审批规则——所有工具必须全部命中
//
// 配置优先级：Agent 级别 > 全局默认 > 内置规则（受 IgnoreDefaultRules 控制）
// 编译/运行时错误通过第二个返回值传播，不会被静默吞咽。
func EvaluateApprovalRules(session *storageStructs.Chats, toolCalls []ToolCall) (ApprovalResult, error) {
	if session == nil || len(toolCalls) == 0 {
		return ApprovalResult{Decision: DecisionManual}, nil
	}

	// 先读取 Agent 级别的配置，作为最高优先级。
	// CurrentAgentConfig 仅在子代理活跃时有效；主会话必须直接使用全局规则。
	autoApprove := ""
	autoReject := ""
	if session.CurrentAgentID != "" || session.NowAgent != "" {
		autoApprove = strings.TrimSpace(session.CurrentAgentConfig.AutoApprove)
		autoReject = strings.TrimSpace(session.CurrentAgentConfig.AutoReject)
		logger.Debug("EvaluateRules: active agent=%q, AutoApprove=%q, AutoReject=%q",
			session.CurrentAgentID, autoApprove, autoReject)
	}

	// 配置优先级：活跃 Agent 配置 > 全局默认配置。
	// 主会话没有活跃 Agent 时始终从全局规则开始。
	if autoApprove == "" {
		autoApprove = strings.TrimSpace(config.GlobalConfig.Agent.DefaultAutoApprove)
		logger.Debug("EvaluateRules: using DefaultAutoApprove=%q", autoApprove)
	}
	if autoReject == "" {
		autoReject = strings.TrimSpace(config.GlobalConfig.Agent.DefaultAutoReject)
		logger.Debug("EvaluateRules: using DefaultAutoReject=%q", autoReject)
	}

	// 系统内置的默认规则
	useBuiltin := !config.GlobalConfig.Agent.IgnoreDefaultRules
	builtinAutoApprove := ""
	builtinAutoReject := ""
	if useBuiltin {
		builtinAutoApprove = strings.TrimSpace(builtinAutoApproveExpr)
		builtinAutoReject = strings.TrimSpace(builtinAutoRejectExpr)
	}

	logger.Debug("EvaluateRules: builtin approve=%q, reject=%q (useBuiltin=%v)",
		builtinAutoApprove, builtinAutoReject, useBuiltin)

	// 用户规则与内置规则使用逻辑或合并
	autoApprove = mergeAutoRuleExpr(autoApprove, builtinAutoApprove)
	autoReject = mergeAutoRuleExpr(autoReject, builtinAutoReject)

	logger.Debug("EvaluateRules: merged approve expr=%q", autoApprove)
	logger.Debug("EvaluateRules: merged reject expr=%q", autoReject)

	var approveProgram *vm.Program
	var rejectProgram *vm.Program
	var err error

	if autoReject != "" {
		rejectProgram, err = compileExpr(autoReject)
		if err != nil {
			logger.Error("EvaluateRules: compile reject rule error: %v", err)
			return ApprovalResult{}, err
		}
	}
	if autoApprove != "" {
		approveProgram, err = compileExpr(autoApprove)
		if err != nil {
			logger.Error("EvaluateRules: compile approve rule error: %v", err)
			return ApprovalResult{}, err
		}
	}

	// 将所有工具调用转为 map 形式供表达式引擎求值
	callsMap := make([]map[string]any, len(toolCalls))
	for i, c := range toolCalls {
		callsMap[i] = c.AsMap()
	}

	// 第 1 层：拒绝检查（安全优先）
	if rejectProgram != nil {
		for _, call := range toolCalls {
			result, runErr := expr.Run(rejectProgram, map[string]any{
				"ToolCalls": callsMap,
				"ToolCall":  call.AsMap(),
				"Agent":     session.CurrentAgentConfig,
			})
			if runErr != nil {
				logger.Error("EvaluateRules: run reject expr error: %v", runErr)
				return ApprovalResult{}, runErr
			}
			if exprTruthy(result) {
				reason := "auto-rejected by reject rule for tool: " + call.Name
				logger.Info("EvaluateRules: %s", reason)
				return ApprovalResult{Decision: DecisionRejected, Reason: reason}, nil
			}
		}
	}

	// 第 2 层：审批规则缺失检查
	if approveProgram == nil {
		logger.Debug("EvaluateRules: no approve rules configured → DecisionManual")
		return ApprovalResult{Decision: DecisionManual}, nil
	}

	// 第 3 层：全量审批检查
	for _, call := range toolCalls {
		result, runErr := expr.Run(approveProgram, map[string]any{
			"ToolCalls": callsMap,
			"ToolCall":  call.AsMap(),
			"Agent":     session.CurrentAgentConfig,
		})
		if runErr != nil {
			logger.Error("EvaluateRules: run approve expr error: %v", runErr)
			return ApprovalResult{}, runErr
		}
		if !exprTruthy(result) {
			logger.Info("EvaluateRules: approve NOT matched for tool: %s → DecisionManual", call.Name)
			return ApprovalResult{Decision: DecisionManual}, nil
		}
	}

	logger.Info("EvaluateRules: all tool calls approved")
	return ApprovalResult{Decision: DecisionApproved}, nil
}

// CanAutoApprove 根据配置的表达式规则判断一组工具调用是否可以自动执行。
// 三层决策逻辑：
//  1. 拒绝检查：任一工具命中拒绝规则则整体驳回（安全优先）
//  2. 审批检查：无规则默认不自动执行（防默许危险操作）
//  3. 全量检查：所有工具都必须触发审批规则
//
// 配置优先级：Agent 级别 > 全局默认 > 系统内置规则（IgnoreDefaultRules=false 时启用）
//
// Deprecated: 使用 EvaluateApprovalRules 替代
func CanAutoApprove(session *storageStructs.Chats, toolCalls []ToolCall, msg *storageStructs.Messages) (bool, string, error) {
	if session == nil || msg == nil || len(toolCalls) == 0 {
		return false, "", nil
	}
	result, err := EvaluateApprovalRules(session, toolCalls)
	if err != nil {
		return false, "", err
	}
	switch result.Decision {
	case DecisionApproved:
		return true, "", nil
	case DecisionRejected:
		return false, result.Reason, nil
	case DecisionManual:
		return false, "", nil
	default:
		return false, "", nil
	}
}

// compileExpr 编译表达式字符串为可执行程序，并注入规则引擎使用的自定义函数。
// 注入函数说明：
//
//	truthy(value)       - 将任意值转为布尔值（委托给 Go 的 exprTruthy）
//	regex(pattern, text)  - 检测参数中是否匹配自定义正则
//	contains(s, sub)     - 关键字匹配，用于检查参数内容（如文件路径关键字）
//	hasParam(call, key)  - 检查工具调用是否存在指定参数名
//	param(call, key)     - 获取工具调用的指定参数值，支持链式调用
//
// ToolCalls 是全集（所有待审批工具），ToolCall 是当前待评估的工具，
// Agent 包含当前 Agent 的上下文配置。这些作为表达式求值环境变量注入。
func compileExpr(program string) (*vm.Program, error) {
	return expr.Compile(program, expr.Env(map[string]any{
		"ToolCalls": []map[string]any{},
		"ToolCall":  map[string]any{},
		"Agent":     cfgStructs.AgentConfig{},
	}), expr.Function("truthy", func(params ...any) (any, error) {
		if len(params) == 0 {
			return false, nil
		}
		return exprTruthy(params[0]), nil
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

// ParseToolsFromJSON 解析工具调用 JSON 字符串为 ToolCall 结构体切片。
// 支持完整 map 和 ObjectSlot（流式解析未完成状态）两种对象形式，
// 以及完整数组和 ArraySlot 两种容器形式。空 payload 返回空切片而非错误。
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
	logger.Info("RejectToolCallsNoDeactivate: chatID=%d, reason=%q", session.ID, reason)
	refer := storageStructs.MessagesReferList{}
	if refers != nil {
		refer = *refers
	}
	finalReason, err := prompts.Render(prompts.UserRejectTemplate, reason)
	if err != nil {
		return err
	}
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

// ApplyToolOnHooks 应用工具调用，遍历所有已解析的工具调用并执行对应的 OnHook 回调
func ApplyToolOnHooks(session *storageStructs.Chats, toolCallingJSON string) error {
	if toolCallingJSON == "" {
		return nil
	}
	toolCalls, err := ParseToolsFromJSON(toolCallingJSON)
	if err != nil {
		return err
	}
	for _, call := range toolCalls {
		session.CurrentToolID = fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, call.ID)
		if err := tools.ExecToolOnHook(session, call.Name, call.Parameters, call.ID); err != nil {
			return err
		}
	}
	return nil
}

// ExecuteToolCalls 执行工具调用并持久化结果。
// 流程：设置状态为 ToolCalling → 逐工具执行 OnHook → 通过 Solver 解析并保存工具响应 → 恢复 Idle 状态。
// 任一环节失败都会回滚到 Idle 并返回错误。
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

// SendRequest 发送 LLM 请求并处理流式响应。
// 流程：设置状态 → 构建请求体 → 发送请求 → 流式解析 → 持久化 → 处理工具调用。
// token 使用阈值刷写策略（每 256 字符批量写库）平衡实时性与 I/O 性能。
// 返回的 bool 值表示是否还有后续工具调用需要处理。
func SendRequest(ctx context.Context, session *storageStructs.Chats, callback func(string, string, uint64, structs.Usage, *string) error) (bool, error) {
	session.State = state.StateWaiting
	session.TemporyDataOfRequest = make(map[string]any)
	session.ToolState = 0
	db := session.DB

	// 确定使用的模型 ID：优先使用子代理配置的模型，否则使用会话最后选择的模型
	modelID := session.LastModelID
	if session.CurrentAgentID != "" {
		modelIDRet := uint32(session.CurrentAgentConfig.AgentModel)
		if modelIDRet != 0 {
			modelID = modelIDRet
		}
	}
	modelCfg, ok := config.GlobalConfig.Model.Models[int32(modelID)]
	if !ok {
		return true, errors.New("model not found")
	}
	logger.Info("SendRequest: using model %s (ID: %d)", modelCfg.ModelName, modelID)

	// var agentConfig *cfgStruct.AgentConfig = nil
	// if agentID != "" {
	// 	agentConfig, err = getAgentConfig(agentID)
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// }

	solver := response.NewSolver(db, session)
	agent := session.CurrentAgentID
	// 在数据库中创建一条空的 Messages 记录作为本次请求的占位符
	// 后续流式响应内容会逐步更新该记录的各个字段
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
	if tx.Error != nil {
		return true, tx.Error
	}

	// session.CurrentMessageID 用于后续工具调用关联到本次消息
	session.CurrentMessageID = reqObj.ID

	var gDelta strings.Builder
	var gThinkingDelta strings.Builder
	var pendingDelta strings.Builder
	var pendingThinkingDelta strings.Builder
	var lastFlushLen int
	var lastFlushThinkingLen int
	msgID := reqObj.ID
	// tokenFlushThreshold 定义了向数据库刷新消息内容的阈值（256 字符）。
	// 流式响应中每收到一个 token 就写数据库会造成严重 I/O 瓶颈。
	// 累积到阈值再统一更新可大幅提升吞吐量（约 100x），
	// 同时保证进程在异常终止时能够保留尽可能多的内容
	const tokenFlushThreshold = 256

	// Usage 信息
	var promptUsage uint32
	var completionUsage uint32
	var totalUsage uint32
	var cachedUsage uint32

	// solveFunc 是 SimpleOpenAIRequest 的回调函数，每次收到流式响应体时调用。
	// 内部处理：增量解析 token → 累积内容 → 达到阈值时写库 → 实时推送到 UI
	solveFunc := func(body structs.ChatCompletionResponse) error {
		if session.State == state.StateRequesting {
			session.State = state.StateReciving
		}
		// 先累积 token 用量：OpenAI/DeepSeek 在流末尾发送 choices 为空的纯 usage 帧，
		// 必须在 choices==0 提前返回之前处理，否则用量统计恒为 0、自动压缩永不触发。
		if body.Usage != nil {
			promptUsage = max(promptUsage, body.Usage.PromptTokens)
			completionUsage = max(completionUsage, body.Usage.CompletionTokens)
			totalUsage = max(totalUsage, body.Usage.TotalTokens)
			cachedUsage = max(cachedUsage, body.Usage.CachedTokens)
			cachedUsage = max(cachedUsage, body.Usage.DeepseekCachedToken)
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
		// 打回检测：模型绕过 <tools> 标签直接输出原生 tool calling 格式时，
		// 立即中止本次流式响应。该响应不会被采用，SendRequest 会删除占位消息、
		// 注入格式纠正消息并重试。检测放在 AddToken 之后、flush 之前，
		// 保证检测到原生格式时占位消息尚未被写入数据库。
		if solver.DetectNativeToolCall() {
			return errNativeToolCallFormat
		}

		// 达到阈值时执行数据库更新（批量刷写，减少 I/O 次数）
		shouldFlush := pendingDelta.Len()+pendingThinkingDelta.Len() >= tokenFlushThreshold
		if shouldFlush {
			gstring := gDelta.String()
			gtstring := gThinkingDelta.String()
			if err := db.Model(&storageStructs.Messages{}).Where("id = ?", msgID).Updates(storageStructs.Messages{
				Delta:            gstring,
				ThinkingDelta:    gtstring,
				PromptTokens:     promptUsage,
				CompletionTokens: completionUsage,
				TotalTokens:      totalUsage,
				CachedTokens:     cachedUsage,
			}).Error; err != nil {
				return err
			}
			pendingDelta.Reset()
			pendingThinkingDelta.Reset()
			// 记录最后一次刷写时的内容长度，用于后续判断是否需要额外更新
			lastFlushLen = len(gstring)
			lastFlushThinkingLen = len(gtstring)
		}
		// 回调函数将增量内容实时推送到 UI 界面（通过 Callback）
		if err := callback(delta, thinkingDelta, msgID, structs.Usage{
			PromptTokens:     promptUsage,
			CompletionTokens: completionUsage,
			TotalTokens:      totalUsage,
			CachedTokens:     cachedUsage,
		}, &session.CurrentAgentID); err != nil {
			return err
		}
		return nil
	}

	session.State = state.StateGeneratingPrompt
	logger.Debug("SendRequest: generating prompt for chat %d", session.ID)
	obj, err := build.Build(db, session)
	if err != nil {
		// 构建失败时删除占位消息，避免空 assistant 消息残留 DB 污染后续上下文
		if delErr := db.Delete(&storageStructs.Messages{}, msgID).Error; delErr != nil {
			logger.Error("delete placeholder message %d: %v", msgID, delErr)
		}
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

	// 创建安全 key 上下文引擎：请求出站脱敏 + 响应流式还原（未启用时返回 nil，零行为变化）
	eng := mask.NewEngine(session.DB)

	// 向 LLM 发送请求，solveFunc 会在每个流式 chunk 到达时被调用
	requestErr := SimpleOpenAIRequest(ctx, modelCfg.ProviderURL, modelCfg.ProviderKey, modelCfg.ModelID, *obj, eng, solveFunc)

	// 打回：模型输出了原生 tool calling 格式（而非 <tools> 标签），本次响应不被采用。
	// 删除占位消息并注入格式纠正消息，返回 (false, nil) 让 loop 重新发起请求，
	// 模型将在下一轮看到纠正反馈并改用 <tools> 标签。
	if errors.Is(requestErr, errNativeToolCallFormat) {
		logger.Warn("native tool calling format detected, rejecting response in chat %d", session.ID)
		session.ClearToolCalling()
		if delErr := db.Delete(&storageStructs.Messages{}, msgID).Error; delErr != nil {
			logger.Error("delete placeholder message %d: %v", msgID, delErr)
		}
		if injErr := injectNativeFormatCorrection(db, session); injErr != nil {
			logger.Error("inject native format correction: %v", injErr)
			return true, injErr
		}
		return false, nil
	}

	isCancel := requestErr != nil && errors.Is(requestErr, context.Canceled)

	// 取消时在 goroutine 中异步完成最后一批内容的持久化，然后立即返回
	// goroutine 中加 recover() + context.Done() 保护，防止 DB 已关闭时 panic
	if isCancel {
		// 取消时仍要异步完成最后一批内容的持久化。
		// 用 WaitGroup 等待写库完成后再返回，保证内容不因进程/DB 提前关闭而丢失；
		// goroutine 内加 recover 防护 DB 已关闭时可能出现的 panic。
		var wg sync.WaitGroup
		wg.Add(1)
		go func(msgID uint64, finalDelta, finalThinkingDelta, toolCallingJSON string,
			promptUsage, completionUsage, totalUsage, cachedUsage uint32,
			lastFlushLen, lastFlushThinkingLen int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("cancel persist goroutine recovered: %v", r)
				}
			}()
			_, delta, thinkingDelta, _ := solver.DoneToken()
			fd := finalDelta + delta
			ftd := finalThinkingDelta + thinkingDelta
			if len(fd) != lastFlushLen || len(ftd) != lastFlushThinkingLen {
				if err := db.Model(&storageStructs.Messages{}).Where("id = ?", msgID).Updates(storageStructs.Messages{
					Delta:            fd,
					ThinkingDelta:    ftd,
					PromptTokens:     promptUsage,
					CompletionTokens: completionUsage,
					TotalTokens:      totalUsage,
					CachedTokens:     cachedUsage,
				}).Error; err != nil {
					logger.Error("cancel persist final content: %v", err)
				}
			}
			if toolCallingJSON != "" {
				if err := db.Model(&storageStructs.Messages{}).Where("id = ?", msgID).Update("tool_calling_json_string", toolCallingJSON).Error; err != nil {
					logger.Error("cancel persist tools origin: %v", err)
				}
			}
		}(msgID, gDelta.String(), gThinkingDelta.String(), solver.GetToolsOrigin(),
			promptUsage, completionUsage, totalUsage, cachedUsage,
			lastFlushLen, lastFlushThinkingLen)
		wg.Wait()
		return true, requestErr
	}

	// 非取消路径（正常完成 或 其他错误）：同步持久化
	ok, delta, thinkingDelta, solverErr := solver.DoneToken()
	if solverErr != nil {
		return true, solverErr
	}
	gDelta.WriteString(delta)
	tools := solver.GetTools()
	gThinkingDelta.WriteString(thinkingDelta)
	// 处理响应：无内容且无工具调用时删除占位消息记录
	if gDelta.String() == "" && gThinkingDelta.String() == "" && len(tools) == 0 {
		// 空响应时删除占位消息，不保留无意义的记录
		if err := db.Delete(&storageStructs.Messages{}, msgID).Error; err != nil {
			return true, err
		}
	} else {
		finalDelta := gDelta.String()
		finalThinkingDelta := gThinkingDelta.String()
		// 仅当最后一次刷写后有新内容时才执行数据库更新，避免冗余 I/O
		if len(finalDelta) != lastFlushLen || len(finalThinkingDelta) != lastFlushThinkingLen {
			if err := db.Model(&storageStructs.Messages{}).Where("id = ?", msgID).Updates(storageStructs.Messages{
				Delta:            finalDelta,
				ThinkingDelta:    finalThinkingDelta,
				PromptTokens:     promptUsage,
				CompletionTokens: completionUsage,
				TotalTokens:      totalUsage,
				CachedTokens:     cachedUsage,
			}).Error; err != nil {
				return true, err
			}
		}
		// 保存工具调用的原始 JSON 字符串，用于后续审批和执行
		if err := db.Model(&storageStructs.Messages{}).Where("id = ?", msgID).Update(
			"tool_calling_json_string", string(solver.GetToolsOrigin()),
		).Error; err != nil {
			return true, err
		}
	}

	// 请求发生其他错误时，内容已持久化完毕，只需将错误向上传递
	if requestErr != nil {
		return true, requestErr
	}

	// 有工具调用时转入审批等待状态，暂停回复处理直到用户或自动规则做出决定
	if len(tools) > 0 {
		// 清空流式增量阶段可能被限流跳过的最后残留（最后一次 OnHook 写但未广播）。
		// 审批展示走 DB 的 tool_calling_json_string，审批通过后 ExecuteToolCalls 会重新写入。
		session.ClearToolCalling()
		session.State = state.StateWaitApprove
		if saveErr := db.Save(session).Error; saveErr != nil {
			return true, saveErr
		}
		return true, nil
	}
	err = callback(delta, thinkingDelta, msgID, structs.Usage{
		PromptTokens:     promptUsage,
		CompletionTokens: completionUsage,
		TotalTokens:      totalUsage,
		CachedTokens:     cachedUsage,
	}, &session.CurrentAgentID)
	if err != nil {
		return true, err
	}

	logger.Debug("[tool body] %s", solver.GetToolsOrigin())
	return ok, nil
}
