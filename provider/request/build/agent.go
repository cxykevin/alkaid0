package build

import (
	"errors"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
)

func getAgentConfig(agentID string) (*structs.AgentConfig, error) {
	agentConfig, ok := config.GlobalConfig.Agent.Agents[agentID]
	if !ok {
		return nil, errors.New("Agent not found")
	}
	return &agentConfig, nil
}
