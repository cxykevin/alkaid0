package agents

import (
	"context"
	"errors"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/values"
)

// ActivateAgent 激活Agent
func ActivateAgent(chatID uint32, agentCode string, prompt string) error {
	// 取agent表
	obj := structs.SubAgents{}
	err := storage.DB.Where("id = ?", agentCode).First(obj).Error
	if err != nil {
		return err
	}

	// 取agent配置
	agentConfig, ok := config.GlobalConfig.Agent.Agents[obj.AgentID]
	if !ok {
		return errors.New("Agent not found")
	}

	// 更新当前Agent
	err = storage.DB.Model(&structs.Chats{}).Where("id = ?", chatID).Update("now_agent", agentCode).Error
	if err != nil {
		return err
	}
	// 提示词写入
	err = storage.DB.Create(&structs.Messages{
		ChatID:  chatID,
		Delta:   prompt,
		AgentID: &agentCode,
		Type:    structs.MessagesRoleUser,
	}).Error
	if err != nil {
		return err
	}

	// 设置值
	values.CurrentActivatePath = obj.BindPath
	CurrentAgentCode = agentCode
	CurrentAgentID = obj.ID
	CurrentAgentConfig = agentConfig
	return nil
}

// DeactivateAgent 取消激活Agent
func DeactivateAgent(chatID uint32) error {
	oldAgent := CurrentAgentCode
	// 更新当前Agent
	err := storage.DB.Model(&structs.Chats{}).Where("id = ?", chatID).Update("now_agent", "").Error
	if err != nil {
		return err
	}
	// 计算summary
	go request.Summary(context.Background(), chatID, oldAgent)
	return nil
}
