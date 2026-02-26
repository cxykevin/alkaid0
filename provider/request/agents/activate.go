package agents

import (
	"context"
	"errors"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/request"
	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// ActivateAgent 激活Agent
func ActivateAgent(session *structs.Chats, agentCode string, prompt string) error {
	// 取agent表
	obj := structs.SubAgents{}
	err := session.DB.Where("id = ?", agentCode).First(&obj).Error
	if err != nil {
		return err
	}

	// 取agent配置
	agentConfig, ok := agentconfig.GetAgentConfig(obj.AgentID)
	if !ok {
		return errors.New("Agent not found")
	}

	// // 更新当前Agent
	// err = session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("now_agent", agentCode).Error
	// if err != nil {
	// 	return err
	// }
	// 提示词写入
	err = session.DB.Create(&structs.Messages{
		ChatID:  session.ID,
		Delta:   prompt,
		AgentID: &agentCode,
		Type:    structs.MessagesRoleCommunicate,
	}).Error
	if err != nil {
		return err
	}

	// 设置值
	session.CurrentActivatePath = obj.BindPath
	session.NowAgent = agentCode
	session.CurrentAgentID = obj.ID
	session.CurrentAgentConfig = agentConfig

	// 写DB
	err = session.DB.Save(session).Error
	if err != nil {
		return err
	}
	return nil
}

// DeactivateAgent 取消激活Agent
func DeactivateAgent(session *structs.Chats, prompt string) error {
	oldAgent := session.NowAgent
	// 更新当前Agent
	err := session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("now_agent", "").Error
	if err != nil {
		return err
	}

	if prompt != "" {
		// 提示词写入
		defaultStr := ""
		err = session.DB.Create(&structs.Messages{
			ChatID:  session.ID,
			Delta:   prompt,
			AgentID: &defaultStr,
			Type:    structs.MessagesRoleCommunicate,
		}).Error
		if err != nil {
			return err
		}
	}

	// 计算summary
	session.NowAgent = ""
	go request.Summary(context.Background(), session.DB, session.ID, oldAgent)

	session.CurrentActivatePath = ""
	session.CurrentAgentID = ""
	session.CurrentAgentConfig = cfgStruct.AgentConfig{}
	return nil
}
