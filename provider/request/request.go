package request

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
	"github.com/cxykevin/alkaid0/provider/request/build"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/provider/response"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// UserAddMsg 用户发送消息
func UserAddMsg(session *structs.Chats, msg string, refers *structs.MessagesReferList) error {
	db := session.DB
	chatID := session.ID
	var refer structs.MessagesReferList
	if refers == nil {
		refer = structs.MessagesReferList{}
	} else {
		refer = *refers
	}

	if session.CurrentAgentID != "" {
		err := actions.DeactivateAgent(session, "<| user stopped subagent |>")
		if err != nil {
			return err
		}
	}

	// 插入
	err := db.Create(&structs.Messages{
		ChatID: chatID,
		Delta:  msg,
		Refers: refer,
		Type:   structs.MessagesRoleUser,
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

type aiToolsResponseTemplate struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	Parameters string `json:"parameters,omitempty"`
}

// SendRequest 发送请求
func SendRequest(ctx context.Context, session *structs.Chats, callback func(string, string) error) (bool, error) {
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
	obj, err := build.Build(db, session)
	if err != nil {
		return true, err
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
	reqObj := structs.Messages{
		ChatID:        session.ID,
		AgentID:       &agent,
		Delta:         "",
		ThinkingDelta: "",
		Type:          structs.MessagesRoleAgent,
		ModelID:       modelID,
		ModelName:     modelCfg.ModelName,
	}
	tx := db.Create(&reqObj)
	// 取主键
	if tx.Error != nil {
		return true, err
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
			if err := db.Model(&structs.Messages{}).Where("id = ?", msgID).Updates(structs.Messages{
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
	if len(tools) > 0 {
		toolsRender := []aiToolsResponseTemplate{}
		for _, v := range tools {
			toolsRender = append(toolsRender, aiToolsResponseTemplate{
				Name:       v.Name,
				ID:         v.ID,
				Parameters: "(omitted content)",
			})
		}
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetIndent("", "    ")
		encoder.SetEscapeHTML(false)
		err = encoder.Encode(toolsRender)
		if err != nil {
			return true, err
		}
	}
	gThinkingDelta.WriteString(thinkingDelta)
	if gDelta.String() == "" && gThinkingDelta.String() == "" {
		// 删除
		err = db.Delete(&structs.Messages{}, msgID).Error
	} else {
		finalDelta := gDelta.String()
		finalThinkingDelta := gThinkingDelta.String()
		if len(finalDelta) != lastFlushLen || len(finalThinkingDelta) != lastFlushThinkingLen {
			err = db.Model(&structs.Messages{}).Where("id = ?", msgID).Updates(structs.Messages{
				Delta:         finalDelta,
				ThinkingDelta: finalThinkingDelta,
			}).Error
		}
		if err == nil {
			err = db.Model(&structs.Messages{}).Where("id = ?", msgID).Update(
				"tool_calling_json_string", string(solver.GetToolsOrigin()),
			).Error
		}
	}
	if err != nil {
		return true, err
	}
	err = callback(delta, thinkingDelta)
	if err != nil {
		return true, err
	}

	logger.Debug("[tool body] %s", solver.GetToolsOrigin())
	return ok, nil
}
