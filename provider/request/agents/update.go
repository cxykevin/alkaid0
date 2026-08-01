package agents

import (
	"errors"

	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// UpdateAgent 更新Agent对象
func UpdateAgent(session *structs.Chats, agentCode string, agentID string, path string) error {
	// 与 AddAgent 一致：先校验绑定路径，防止绕过校验逃逸绑定路径/工作区根目录
	if err := validateBindPath(path); err != nil {
		return err
	}
	// 校验 agentID 可解析，避免把子代理更新为不可加载的 tag 导致会话无法打开
	if _, ok := agentconfig.GetAgentConfig(agentID); !ok {
		return errors.New("agent id not found")
	}

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
