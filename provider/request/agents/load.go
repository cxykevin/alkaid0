package agents

import (
	"errors"

	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// LoadAgent 加载 Agent
func LoadAgent(session *structs.Chats) error {
	if session.NowAgent == "" {
		return nil
	}
	// 取agent表
	obj := structs.SubAgents{}
	err := session.DB.Where("id = ?", session.NowAgent).First(&obj).Error
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

	// 设置值
	session.CurrentActivatePath = obj.BindPath
	session.CurrentAgentID = obj.ID
	session.CurrentAgentConfig = agentConfig

	// 写DB：这些字段都是 gorm:"-"，不产生列更新；这里只把已持久化的 now_agent
	// 重新写一遍刷新 updated_at，避免整行 Save 覆盖并发写入的 ai_title（P1-18 附带项）。
	err = session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).
		Update("now_agent", session.NowAgent).Error
	if err != nil {
		return err
	}
	return nil
}
