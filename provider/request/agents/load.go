package agents

import (
	"errors"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// LoadAgent 加载 Agent
func LoadAgent(session *structs.Chats) error {
	// 取agent表
	obj := structs.SubAgents{}
	err := session.DB.Where("id = ?", session.NowAgent).First(&obj).Error
	if err != nil {
		return err
	}

	// 取agent配置
	agentConfig, ok := config.GlobalConfig.Agent.Agents[obj.AgentID]
	if !ok {
		return errors.New("Agent not found")
	}

	// // 更新当前Agent
	// err = session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("now_agent", agentCode).Error
	// if err != nil {
	// 	return err
	// }
	// 提示词写入

	// 设置值
	session.CurrentActivatePath = obj.BindPath
	session.CurrentAgentID = obj.ID
	session.CurrentAgentConfig = agentConfig

	// 写DB
	err = session.DB.Save(session).Error
	if err != nil {
		return err
	}
	return nil
}
