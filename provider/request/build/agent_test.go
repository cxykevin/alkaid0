package build

import (
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
)

// TestGetAgentConfig_Success 测试成功获取代理配置
func TestGetAgentConfig_Success(t *testing.T) {
	// 设置测试配置
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			Agents: map[string]cfgStruct.AgentConfig{
				"test-agent": {
					AgentName:        "Test Agent",
					AgentPrompt:      "You are a test agent",
					AgentModel:       1,
					AgentDescription: "A test agent",
				},
			},
		},
	}

	// 测试获取存在的代理
	agentCfg, err := getAgentConfig("test-agent")
	if err != nil {
		t.Fatalf("getAgentConfig failed: %v", err)
	}

	if agentCfg == nil {
		t.Fatal("Expected agent config, got nil")
	}

	if agentCfg.AgentName != "Test Agent" {
		t.Errorf("Expected agent name 'Test Agent', got '%s'", agentCfg.AgentName)
	}

	if agentCfg.AgentPrompt != "You are a test agent" {
		t.Errorf("Expected agent prompt 'You are a test agent', got '%s'", agentCfg.AgentPrompt)
	}
}

// TestGetAgentConfig_NotFound 测试获取不存在的代理
func TestGetAgentConfig_NotFound(t *testing.T) {
	// 设置测试配置
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			Agents: map[string]cfgStruct.AgentConfig{
				"test-agent": {
					AgentName: "Test Agent",
				},
			},
		},
	}

	// 测试获取不存在的代理
	agentCfg, err := getAgentConfig("non-existent-agent")
	if err == nil {
		t.Fatal("Expected error for non-existent agent, got nil")
	}

	if agentCfg != nil {
		t.Errorf("Expected nil agent config, got %v", agentCfg)
	}

	if err.Error() != "Agent not found" {
		t.Errorf("Expected error 'Agent not found', got '%s'", err.Error())
	}
}

// TestGetAgentConfig_EmptyAgentID 测试空代理ID
func TestGetAgentConfig_EmptyAgentID(t *testing.T) {
	// 设置测试配置
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			Agents: map[string]cfgStruct.AgentConfig{
				"test-agent": {
					AgentName: "Test Agent",
				},
			},
		},
	}

	// 测试空代理ID
	agentCfg, err := getAgentConfig("")
	if err == nil {
		t.Fatal("Expected error for empty agent ID, got nil")
	}

	if agentCfg != nil {
		t.Errorf("Expected nil agent config, got %v", agentCfg)
	}
}

// TestGetAgentConfig_MultipleAgents 测试多个代理配置
func TestGetAgentConfig_MultipleAgents(t *testing.T) {
	// 设置测试配置
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			Agents: map[string]cfgStruct.AgentConfig{
				"agent1": {
					AgentName:   "Agent 1",
					AgentPrompt: "Prompt 1",
				},
				"agent2": {
					AgentName:   "Agent 2",
					AgentPrompt: "Prompt 2",
				},
				"agent3": {
					AgentName:   "Agent 3",
					AgentPrompt: "Prompt 3",
				},
			},
		},
	}

	// 测试获取每个代理
	testCases := []struct {
		agentID      string
		expectedName string
	}{
		{"agent1", "Agent 1"},
		{"agent2", "Agent 2"},
		{"agent3", "Agent 3"},
	}

	for _, tc := range testCases {
		agentCfg, err := getAgentConfig(tc.agentID)
		if err != nil {
			t.Errorf("getAgentConfig(%s) failed: %v", tc.agentID, err)
			continue
		}

		if agentCfg.AgentName != tc.expectedName {
			t.Errorf("Expected agent name '%s', got '%s'", tc.expectedName, agentCfg.AgentName)
		}
	}
}
