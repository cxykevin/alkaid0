package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/request"
	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// GoalCheckAgentID 内置 goal-check 代理的 tag（见 builtins.json）。
const GoalCheckAgentID = "goal-check"

// GoalCheckInstanceID goal-check 的固定实例 ID。
// 消息回放按 agent_id 过滤，固定 ID 让每轮校验都能看到之前的校验历史
// （历史已被 compress 机制处理），而不是每次从零开始。
const GoalCheckInstanceID = structs.GoalCheckInstanceID

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

	// 取agent配置（内部代理不能被 AI 激活，按不存在处理）
	agentConfig, ok := agentconfig.GetAgentConfig(obj.AgentID)
	if !ok || agentConfig.Internal {
		return errors.New("Agent not found")
	}

	// // 更新当前Agent
	// err = session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("now_agent", agentCode).Error
	// if err != nil {
	// 	return err
	// }
	// 提示词写入
	err = structs.AppendMessage(session.DB, &structs.Messages{
		ChatID:  session.ID,
		Delta:   prompt,
		AgentID: &agentCode,
		Type:    structs.MessagesRoleCommunicate,
	})
	if err != nil {
		return err
	}

	// 设置值
	session.CurrentActivatePath = obj.BindPath
	session.NowAgent = agentCode
	session.CurrentAgentID = obj.ID
	session.CurrentAgentConfig = agentConfig

	// 写DB：只更新 now_agent 单列。整行 Save(session) 会把内存里可能已过期的
	// ai_title/title/task 等列一并写回，覆盖标题 goroutine 等并发写入（P1-18 附带项）。
	err = session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).
		Update("now_agent", agentCode).Error
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
		err = structs.AppendMessage(session.DB, &structs.Messages{
			ChatID:  session.ID,
			Delta:   prompt,
			AgentID: &defaultStr,
			Type:    structs.MessagesRoleCommunicate,
		})
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
			if err := structs.AppendMessage(session.DB, &structs.Messages{
				ChatID:  session.ID,
				Delta:   summary,
				AgentID: &mainAgentID,
				Type:    structs.MessagesRoleCommunicate,
			}); err != nil {
				logger.Warn("failed to persist deactivated agent %q summary: %v", oldAgent, err)
			}
		}
	}

	session.CurrentActivatePath = ""
	session.CurrentAgentID = ""
	session.CurrentAgentConfig = cfgStruct.AgentConfig{}
	return nil
}

// ActivateInternalAgent 激活一个内部代理（goal-check）。
//
// 与 ActivateAgent 的区别：不查/写 SubAgents 表，也不写 chats.now_agent——
// 内部代理不是用户可见的实例，不能出现在 <agents> 列表里，也不该跨进程恢复。
// prompt 作为 Communicate 消息落库，供该代理的上下文使用。
func ActivateInternalAgent(session *structs.Chats, agentID, instanceID, prompt string) error {
	if session == nil || session.DB == nil {
		return errors.New("activate internal agent: nil session or db")
	}
	cfg, ok := agentconfig.GetAgentConfig(agentID)
	if !ok || !cfg.Internal {
		return fmt.Errorf("internal agent %q not found", agentID)
	}
	// goal-check 的模型可单独配置；-1/0 表示跟随会话模型（EffectiveModelID 已按 >0 判定）。
	if agentID == GoalCheckAgentID && config.GlobalConfig.Agent.GoalCheckModel > 0 {
		cfg.AgentModel = config.GlobalConfig.Agent.GoalCheckModel
	}
	if instanceID == "" {
		instanceID = agentID
	}

	session.AgentLifecycleLock()
	defer session.AgentLifecycleUnlock()

	if strings.TrimSpace(prompt) != "" {
		agentCode := instanceID
		if err := structs.AppendMessage(session.DB, &structs.Messages{
			ChatID:  session.ID,
			Delta:   prompt,
			AgentID: &agentCode,
			Type:    structs.MessagesRoleCommunicate,
		}); err != nil {
			return err
		}
	}

	session.CurrentActivatePath = ""
	session.NowAgent = instanceID
	session.CurrentAgentID = instanceID
	session.CurrentAgentConfig = cfg
	return nil
}

// ClearInternalAgent 清理内部代理的运行时激活状态（不写库：内部代理从未持久化 now_agent）。
func ClearInternalAgent(session *structs.Chats) {
	if session == nil {
		return
	}
	session.AgentLifecycleLock()
	defer session.AgentLifecycleUnlock()
	session.NowAgent = ""
	session.CurrentAgentID = ""
	session.CurrentActivatePath = ""
	session.CurrentAgentConfig = cfgStruct.AgentConfig{}
}

// LastAgentOutput 返回指定 agent 最近一条非空 assistant 正文。
// agentID 为空表示主 agent。
func LastAgentOutput(session *structs.Chats, agentID string) string {
	if session == nil || session.DB == nil {
		return ""
	}
	query := structs.OnActiveBranch(session.DB, session.ID).
		Where("`type` = ? AND delta != ''", structs.MessagesRoleAgent)
	if agentID == "" {
		query = query.Where("(`agent_id` = '' OR `agent_id` IS NULL)")
	} else {
		query = query.Where("agent_id = ?", agentID)
	}
	var msg structs.Messages
	if err := query.Order("id DESC").Limit(1).Find(&msg).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(msg.Delta)
}

// AbortAgentWithOutput 普通子代理未调用 deactivate_agent 就结束时的兜底：
// 把它最后一段 assistant 正文作为回传消息投递给主 agent；没有正文时投递明确的报错。
// 不执行 Summary（避免重复总结与 120s 阻塞），并清理激活状态。
func AbortAgentWithOutput(session *structs.Chats) error {
	if session == nil || session.DB == nil {
		return errors.New("abort agent: nil session or db")
	}
	session.AgentLifecycleLock()
	defer session.AgentLifecycleUnlock()

	code := session.CurrentAgentID
	if code == "" {
		code = session.NowAgent
	}
	if code == "" {
		return nil
	}
	content := LastAgentOutput(session, code)
	mainAgentID := ""
	var delta string
	if content == "" {
		delta = fmt.Sprintf("[subagent %s ended abnormally without any output]", code)
	} else {
		delta = fmt.Sprintf("[subagent %s returned without deactivate]\n%s", code, content)
	}
	if err := structs.AppendMessage(session.DB, &structs.Messages{
		ChatID:  session.ID,
		Delta:   delta,
		AgentID: &mainAgentID,
		Type:    structs.MessagesRoleCommunicate,
	}); err != nil {
		return err
	}
	session.NowAgent = ""
	session.CurrentAgentID = ""
	session.CurrentActivatePath = ""
	session.CurrentAgentConfig = cfgStruct.AgentConfig{}
	if err := session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).
		Update("now_agent", "").Error; err != nil {
		logger.Warn("failed to clear now_agent for session %d: %v", session.ID, err)
	}
	return nil
}
