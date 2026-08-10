package build

import (
	"container/list"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/prompts"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

const summaryKeepNumber = 6

// Summary 请求总结
func Summary(chatID uint32, agentID string, db *gorm.DB) (uint64, *reqStruct.ChatCompletionRequest, error) {
	keepNum := summaryKeepNumber
	if agentID != "" {
		keepNum = 0
	}
	return SummaryWithKeepNumber(chatID, agentID, db, keepNum)
}

// SummaryWithKeepNumber 请求总结(指定保留条数)
func SummaryWithKeepNumber(chatID uint32, agentID string, db *gorm.DB, keepNum int) (uint64, *reqStruct.ChatCompletionRequest, error) {

	modelConfig, err := GetModelConfig(config.GlobalConfig.Agent.SummaryModel)
	if err != nil {
		return 0, nil, err
	}

	response := &reqStruct.ChatCompletionRequest{}

	// 配置模型信息
	response.Model = modelConfig.ModelID
	response.Stream = true
	if modelConfig.ProviderSpecificConfig.EnableTemperature && modelConfig.ModelTemperature != -1 && modelConfig.ModelTemperature != 0 {
		response.Temperature = &modelConfig.ModelTemperature
	}
	if modelConfig.ProviderSpecificConfig.EnableTopP && modelConfig.ModelTopP != -1 && modelConfig.ModelTopP != 0 {
		response.TopP = &modelConfig.ModelTopP
	}
	var maxTokenObj int = maxToken
	response.MaxTokens = &maxTokenObj

	// 生成 messages
	responseDeltaList := list.New()
	exitFlag := false
	var lastMsgID uint64
	var totalMsgCount int64
	if agentID == "" {
		db.Model(&structs.Messages{}).Where("`chat_id` = ? AND (`agent_id` = \"\" OR `agent_id` IS NULL)", chatID).Count(&totalMsgCount)
	} else {
		db.Model(&structs.Messages{}).Where("`chat_id` = ? AND `agent_id` = ?", chatID, agentID).Count(&totalMsgCount)
	}

	for offsetPage := range maxPage {
		var obj []structs.Messages
		if agentID == "" {
			db.Where("`chat_id` = ? AND (`agent_id` = \"\" OR `agent_id` IS NULL)", chatID).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj)
		} else {
			db.Where("`chat_id` = ? AND `agent_id` = ?", chatID, agentID).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj)
		}
		if len(obj) == 0 {
			break
		}
		for idx, v := range obj {
			// 最近 keepNum 条保持完整（不设置 lastMsgID、不触发 exitFlag），
			// 但仍作为上下文输入给总结模型——否则模型看不到最近的进展，
			// 在 summary 提示词强制 100-300 词的约束下会对缺失内容产生幻觉（瞎编）。
			isRecent := totalMsgCount > int64(keepNum) && offsetPage == 0 && idx < keepNum
			if !isRecent && lastMsgID == 0 {
				lastMsgID = v.ID
			}
			msg := reqStruct.Message{
				Role:    msgRole[v.Type],
				Content: "",
			}
			skipMsg := false
			if v.Summary != "" {
				rendered, err := prompts.Render(prompts.SummaryWrapTemplate, struct {
					Summary string
				}{Summary: v.Summary})
				if err != nil {
					return 0, nil, err
				}
				msg.Content = rendered
				if !isRecent {
					exitFlag = true
				}
			} else {
				if v.Type == structs.MessagesRoleTool || (v.Type == structs.MessagesRoleAgent && v.ToolCallingJSONString != "") {
					// 工具调用信息不进入总结：工具结果消息与带工具调用的 assistant 消息一律跳过，
					// 总结模型只见纯文本对话，不接触任何工具格式（<tools> / <tools_return> / tool_calls）。
					skipMsg = true
				} else if v.Type == structs.MessagesRoleUser {
					rendered, err := prompts.Render(prompts.UserWrapTemplate, struct {
						Prompt string
						Refers structs.MessagesReferList
					}{
						Prompt: v.Delta,
						Refers: v.Refers,
					})
					if err != nil {
						return 0, nil, err
					}
					msg.Content = rendered
				} else if v.Type == structs.MessagesRoleTool {
					toolRendered, err := prompts.Render(prompts.ToolResponseWrapTemplate, struct {
						Prompt string
					}{
						Prompt: v.Delta,
					})
					if err != nil {
						return 0, nil, err
					}
					msg.Content = toolRendered
				} else if v.Type == structs.MessagesRoleCommunicate {
					renderAgentID := ""
					if v.AgentID != nil {
						renderAgentID = *v.AgentID
					}
					if renderAgentID == agentID {
						if agentID == "" {
							agentRendered, err := prompts.Render(prompts.AgentWrapTemplate, struct {
								Prompt string
							}{
								Prompt: v.Delta,
							})
							if err != nil {
								return 0, nil, err
							}
							msg.Content = agentRendered
						} else {
							subAgentRendered, err := prompts.Render(prompts.SubagentWrapTemplate, struct {
								Prompt string
							}{
								Prompt: v.Delta,
							})
							if err != nil {
								return 0, nil, err
							}
							msg.Content = subAgentRendered
						}
					} else {
						// 属于其他会话/子代理的通信消息，不参与本次总结上下文，避免空 user 消息误导模型
						skipMsg = true
					}
				} else if v.ThinkingDelta != "" {
					thinkingWrap := ""
					if modelConfig.EnableThinking {
						thinkingString := v.ThinkingDelta
						msg.ReasoningContent = &thinkingString
						msg.Content = v.Delta
					} else {
						thinkingWrap = v.ThinkingDelta
						deltaRendered, err := prompts.Render(prompts.DeltaWrapTemplate, struct {
							Thinking  string
							Delta     string
							ToolsCall string
						}{
							Thinking:  thinkingWrap,
							Delta:     v.Delta,
							ToolsCall: v.ToolCallingJSONString,
						})
						if err != nil {
							return 0, nil, err
						}
						msg.Content = deltaRendered
					}
				} else {
					msg.Content = v.Delta
				}
			}
			if skipMsg {
				continue
			}
			responseDeltaList.PushFront(msg)
			if exitFlag {
				break
			}
		}
		if exitFlag {
			break
		}
	}

	// 收集到的消息列表
	messages := make([]reqStruct.Message, 0, responseDeltaList.Len()+2)

	// 1. 放入系统提示词
	globalRendered, err := prompts.Render(prompts.GlobalTemplate, struct {
		ModelName string
	}{
		ModelName: modelConfig.ModelName,
	})
	if err != nil {
		return 0, nil, err
	}
	messages = append(messages, reqStruct.Message{
		Role:    "system",
		Content: globalRendered,
	})

	// 2. 放入对话内容
	for j := responseDeltaList.Front(); j != nil; j = j.Next() {
		messages = append(messages, j.Value.(reqStruct.Message))
	}

	// 如果没有对话内容（除了系统提示词），返回 0
	if len(messages) <= 1 {
		return 0, nil, nil
	}

	// 3. 放入总结指令
	messages = append(messages, reqStruct.Message{
		Role:    "user",
		Content: prompts.Summary,
	})

	response.Messages = messages

	if modelConfig.ProviderSpecificConfig.EnableReasoningEffort {
		low := "low"
		response.ReasoningEffort = &low
	}
	if modelConfig.ProviderSpecificConfig.EnableDeepseekThinking {
		response.Thinking = &reqStruct.ChatCompletionThinkingType{
			Type: "disabled",
		}
	}
	return lastMsgID, response, nil
}
