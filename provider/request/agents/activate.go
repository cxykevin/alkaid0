package agents

import (
	"context"
	"errors"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/request"
	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

//var logger = log.New("agents")

// ActivateAgent 激活Agent
func ActivateAgent(session *structs.Chats, agentCode string, prompt string) error {
	if session == nil || session.DB == nil {
		return errors.New("activate agent: nil session or db")
	}
	session.AgentLifecycleLock()
	defer session.AgentLifecycleUnlock()

	logger.Info("activating agent: %s", agentCode)
	// 取agent表
	obj := structs.SubAgents{}
	err := session.DB.Where("id = ?", agentCode).First(&obj).Error
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
	err = session.DB.Create(&structs.Messages{
		ChatID:  session.ID,
		Delta:   prompt,
		AgentID: &agentCode,
		Type:    structs.MessagesRoleCommunicate,
	}).Error
	if err != nil {
		return err
	}

	// 设置值
	session.CurrentActivatePath = obj.BindPath
	session.NowAgent = agentCode
	session.CurrentAgentID = obj.ID
	session.CurrentAgentConfig = agentConfig

	// 写DB
	err = session.DB.Save(session).Error
	if err != nil {
		return err
	}
	return nil
}

// DeactivateAgent 取消激活Agent
func DeactivateAgent(session *structs.Chats, prompt string) error {
	if session == nil || session.DB == nil {
		return errors.New("deactivate agent: nil session or db")
	}
	session.AgentLifecycleLock()
	defer session.AgentLifecycleUnlock()

	logger.Info("deactivating agent: %s", session.NowAgent)
	oldAgent := session.NowAgent
	if oldAgent == "" {
		oldAgent = session.CurrentAgentID
	}
	if oldAgent == "" {
		// A repeated/stale call is already complete and must not write another
		// prompt or start another summary.
		return nil
	}
	hadActiveAgent := true
	// 先在内存中清空激活状态，避免工具调用重入时再次把当前会话识别为活跃子代理。
	// 总结过程可能耗时，状态必须在执行总结前完成切换。
	session.NowAgent = ""
	session.CurrentAgentID = ""
	session.CurrentActivatePath = ""
	session.CurrentAgentConfig = cfgStruct.AgentConfig{}

	// 更新当前Agent
	err := session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("now_agent", "").Error
	if err != nil {
		return err
	}

	if prompt != "" {
		// 提示词写入
		defaultStr := ""
		err = session.DB.Create(&structs.Messages{
			ChatID:  session.ID,
			Delta:   prompt,
			AgentID: &defaultStr,
			Type:    structs.MessagesRoleCommunicate,
		}).Error
		if err != nil {
			return err
		}
	}

	// 先完成子代理总结，再把总结写入主代理消息流。
	// 不能异步执行：调用方会在停用成功后立即构建主代理请求，异步总结可能尚未
	// 写入数据库，导致主代理看不到子代理的回传内容。
	session.NowAgent = ""
	sumCtx := session.GetContext()
	if sumCtx == nil {
		sumCtx = context.Background()
	}
	if hadActiveAgent {
		if summary, summaryErr := request.Summary(sumCtx, session.DB, session.ID, oldAgent); summaryErr != nil {
			logger.Warn("failed to summarize deactivated agent %q: %v", oldAgent, summaryErr)
		} else if summary != "" {
			mainAgentID := ""
			if err := session.DB.Create(&structs.Messages{
				ChatID:  session.ID,
				Delta:   summary,
				AgentID: &mainAgentID,
				Type:    structs.MessagesRoleCommunicate,
			}).Error; err != nil {
				logger.Warn("failed to persist deactivated agent %q summary: %v", oldAgent, err)
			}
		}
	}

	session.CurrentActivatePath = ""
	session.CurrentAgentID = ""
	session.CurrentAgentConfig = cfgStruct.AgentConfig{}
	return nil
}
