package request

import (
	"context"
	"errors"
	"strings"

	"github.com/cxykevin/alkaid0/config"
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
	var chat structs.Chats
	db.First(&chat, 1)
	chat.NowAgent = ""
	err := db.Select("NowAgent").Save(&chat).Error
	if err != nil {
		return err
	}
	// 插入
	err = db.Create(&structs.Messages{
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

// SendRequest 发送请求
func SendRequest(ctx context.Context, session *structs.Chats, callback func(string, string) error) (bool, error) {
	db := session.DB
	chatID := session.ID
	// 取模型ID
	var chat structs.Chats
	err := db.First(&chat, chatID).Error
	if err != nil {
		return true, err
	}
	modelCfg, ok := config.GlobalConfig.Model.Models[int32(chat.LastModelID)]
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
	// 写库
	reqObj := structs.Messages{
		ChatID:        chatID,
		AgentID:       &chat.NowAgent,
		Delta:         "",
		ThinkingDelta: "",
		Type:          structs.MessagesRoleAgent,
		ModelID:       chat.LastModelID,
		ModelName:     modelCfg.ModelName,
	}
	tx := db.Create(&reqObj)
	// 取主键
	if tx.Error != nil {
		return true, err
	}
	var gDelta strings.Builder
	var gThinkingDelta strings.Builder
	msgID := reqObj.ID
	solveFunc := func(body reqStruct.ChatCompletionResponse) error {
		if len(body.Choices) == 0 {
			return nil
		}
		delta, thinkingDelta, err := solver.AddToken(body.Choices[0].Delta.Content, stringDefault(body.Choices[0].Delta.ReasoningContent))
		gDelta.WriteString(delta)
		gThinkingDelta.WriteString(thinkingDelta)
		if err != nil {
			return err
		}
		gstring := gDelta.String()
		gtstring := gThinkingDelta.String()
		err = db.Model(&structs.Messages{}).Where("id = ?", msgID).Updates(structs.Messages{
			Delta:         gstring,
			ThinkingDelta: gtstring,
		}).Error
		if err != nil {
			return err
		}
		err = callback(delta, thinkingDelta)
		return err
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
	gThinkingDelta.WriteString(thinkingDelta)
	if gDelta.String() == "" && gThinkingDelta.String() == "" {
		// 删除
		err = db.Delete(&structs.Messages{}, msgID).Error
	} else {
		err = db.Model(&structs.Messages{}).Where("id = ?", msgID).Updates(structs.Messages{
			Delta:         gDelta.String(),
			ThinkingDelta: gThinkingDelta.String(),
		}).Error
	}
	if err != nil {
		return true, err
	}
	err = callback(delta, thinkingDelta)
	if err != nil {
		return true, err
	}
	return ok, nil
}
