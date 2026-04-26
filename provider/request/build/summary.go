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
			// 如果总消息数大于 keepNum，则跳过最近的 keepNum 条
			// 否则全部包含，以确保总结有内容
			if totalMsgCount > int64(keepNum) && offsetPage == 0 && idx < keepNum {
				continue
			}
			if lastMsgID == 0 {
				lastMsgID = v.ID
			}
			msg := reqStruct.Message{
				Role:    msgRole[v.Type],
				Content: "",
			}
			if v.Summary != "" {
				msg.Content = prompts.Render(prompts.SummaryWrapTemplate, struct {
					Summary string
				}{Summary: v.Summary})
				exitFlag = true
			} else {
				if v.Type == structs.MessagesRoleUser {
					msg.Content = prompts.Render(prompts.UserWrapTemplate, struct {
						Prompt string
						Refers structs.MessagesReferList
					}{
						Prompt: v.Delta,
						Refers: v.Refers,
					})
				} else if v.Type == structs.MessagesRoleTool {
					msg.Content = prompts.Render(prompts.ToolResponseWrapTemplate, struct {
						Prompt string
					}{
						Prompt: v.Delta,
					})
				} else if v.Type == structs.MessagesRoleCommunicate {
					renderAgentID := ""
					if v.AgentID != nil {
						renderAgentID = *v.AgentID
					}
					if renderAgentID == agentID {
						if agentID == "" {
							msg.Content = prompts.Render(prompts.AgentWrapTemplate, struct {
								Prompt string
							}{
								Prompt: v.Delta,
							})
						} else {
							msg.Content = prompts.Render(prompts.SubagentWrapTemplate, struct {
								Prompt string
							}{
								Prompt: v.Delta,
							})
						}
					}
				} else if v.ThinkingDelta != "" {
					thinkingWrap := ""
					if modelConfig.EnableThinking {
						thinkingString := v.ThinkingDelta
						msg.ReasoningContent = &thinkingString
						msg.Content = v.Delta
					} else {
						thinkingWrap = v.ThinkingDelta
						msg.Content = prompts.Render(prompts.DeltaWrapTemplate, struct {
							Thinking  string
							Delta     string
							ToolsCall string
						}{
							Thinking:  thinkingWrap,
							Delta:     v.Delta,
							ToolsCall: v.ToolCallingJSONString,
						})
					}
				} else {
					msg.Content = v.Delta
				}
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
	systemContent := prompts.Render(prompts.GlobalTemplate, struct {
		ModelName string
	}{
		ModelName: modelConfig.ModelName,
	})
	messages = append(messages, reqStruct.Message{
		Role:    "system",
		Content: systemContent,
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
		response.ReasoningEffort = new("low")
	}
	if modelConfig.ProviderSpecificConfig.EnableDeepseekThinking {
		response.Thinking = &reqStruct.ChatCompletionThinkingType{
			Type: "disabled",
		}
	}
	return lastMsgID, response, nil
}
