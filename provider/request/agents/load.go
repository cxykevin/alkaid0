package agents

import (
	"errors"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/values"
)

// LoadAgent 加载 Agent
func LoadAgent(agentCode string) error { // 从DB拿到AgentID
	subagentObj := storageStructs.SubAgents{}
	err := storage.DB.Where("id = ?", agentCode).First(&agentCode).Error
	if err != nil {
		return err
	}
	agentConfig, ok := config.GlobalConfig.Agent.Agents[subagentObj.AgentID]
	if !ok {
		return errors.New("Agent not found")
	}
	CurrentAgentID = subagentObj.AgentID
	CurrentAgentConfig = agentConfig
	values.CurrentActivatePath = subagentObj.BindPath
	return nil
}
