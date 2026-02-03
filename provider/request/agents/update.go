package agents

import (
	"github.com/cxykevin/alkaid0/storage/structs"
)

// UpdateAgent 更新Agent对象
func UpdateAgent(session *structs.Chats, agentCode string, agentID string, path string) error {
	var existingAgent structs.SubAgents
	err := session.DB.Where("id = ?", agentCode).First(&existingAgent).Error
	if err != nil {
		return err
	}

	existingAgent.AgentID = agentID
	existingAgent.BindPath = path
	err = session.DB.Save(&existingAgent).Error
	if err != nil {
		return err
	}
	return nil
}
