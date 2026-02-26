package agentconfig

import (
	_ "embed" // embed
	"encoding/json"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/log"
)

//go:embed builtins.json
var builtinsJSONString string

var builtins map[string]structs.AgentConfig

var logger = log.New("agents/builtins")

func init() {
	err := json.Unmarshal([]byte(builtinsJSONString), &builtins)
	logger.Error("load builtin agents failed: %v", err)
}

// GetAgentConfig 获取agent信息
func GetAgentConfig(agentID string) (structs.AgentConfig, bool) {
	val, ok := config.GlobalConfig.Agent.Agents[agentID]
	if !ok {
		val, ok = builtins[agentID]
		return val, ok
	}
	return val, ok
}

// GetAgentConfigMap 获取agent信息
func GetAgentConfigMap() map[string]structs.AgentConfig {
	if config.GlobalConfig.Agent.IgnoreBuiltinAgents {
		return config.GlobalConfig.Agent.Agents
	}
	// 合并map
	return mergeMap(config.GlobalConfig.Agent.Agents, builtins)
}

func mergeMap(a, b map[string]structs.AgentConfig) map[string]structs.AgentConfig {
	result := make(map[string]structs.AgentConfig)
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}
