package agents

import (
	"errors"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// LoadAgent 加载 Agent
func LoadAgent(db *gorm.DB, session *structs.Chats, agentCode string) error { // 从DB拿到AgentID
	subagentObj := storageStructs.SubAgents{}
	err := db.Where("id = ?", agentCode).First(&agentCode).Error
	if err != nil {
		return err
	}
	agentConfig, ok := config.GlobalConfig.Agent.Agents[subagentObj.AgentID]
	if !ok {
		return errors.New("Agent not found")
	}
	session.CurrentAgentID = subagentObj.AgentID
	session.CurrentAgentConfig = agentConfig
	session.CurrentActivatePath = subagentObj.BindPath
	return nil
}
