package request

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
	"github.com/cxykevin/alkaid0/provider/request/build"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/provider/response"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools"
	"github.com/cxykevin/alkaid0/ui/state"
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

// CanAutoApprove 自动审批策略（待补充）
func CanAutoApprove(session *storageStructs.Chats, toolCalls []ToolCall, msg *storageStructs.Messages) (bool, string, error) {
	return false, "", nil
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
	var tools []ToolCall
	if err := json.Unmarshal([]byte(payload), &tools); err != nil {
		return nil, err
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
	encoder := json.NewEncoder(&buf)
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
