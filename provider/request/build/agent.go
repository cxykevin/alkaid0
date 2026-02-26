package build

import (
	"errors"

	"github.com/cxykevin/alkaid0/config/structs"
	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
)

func getAgentConfig(agentID string) (*structs.AgentConfig, error) {
	agentConfig, ok := agentconfig.GetAgentConfig(agentID)
	if !ok {
		return nil, errors.New("Agent not found")
	}
	return &agentConfig, nil
}
