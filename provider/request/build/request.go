package build

import (
	"container/list"
	"encoding/json"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

const readPageSize = 20
const maxPage = 10
const maxToken = 8192

var msgRole = map[structs.MessagesRole]string{
	structs.MessagesRoleUser:        "user",
	structs.MessagesRoleAgent:       "assistant",
	structs.MessagesRoleTool:        "user",
	structs.MessagesRoleCommunicate: "user",
}

// RequestBody 构建请求
func RequestBody(chatID uint32, modelID int32, agentCode string, toolsList *[]*parser.ToolsDefine, db *gorm.DB, addSystemPrompt string, addUserPrompt string, agentCfg cfgStruct.AgentConfig, chatLn storageStructs.Chats) (*reqStruct.ChatCompletionRequest, error) {
	toolsLst, err := json.Marshal(*toolsList)
	if err != nil {
		return nil, err
	}

	modelConfig, err := GetModelConfig(modelID)
	if err != nil {
		return nil, err
	}

	response := &reqStruct.ChatCompletionRequest{}

	// 配置模型信息
	response.Model = modelConfig.ModelID
	response.Stream = true
	if modelConfig.ProviderSpecificConfig.EnableUsage {
		response.StreamOptions = &reqStruct.ChatCompletionStreamOptions{
			IncludeUsage: true,
		}
	}
	if modelConfig.ProviderSpecificConfig.EnableTemperature && modelConfig.ModelTemperature != -1 && modelConfig.ModelTemperature != 0 {
		response.Temperature = &modelConfig.ModelTemperature
	}
	if modelConfig.ProviderSpecificConfig.EnableTopP && modelConfig.ModelTopP != -1 && modelConfig.ModelTopP != 0 {
		response.TopP = &modelConfig.ModelTopP
	}
	var maxTokenObj int = maxToken
	response.MaxTokens = &maxTokenObj
	if modelConfig.ProviderSpecificConfig.EnableDeepseekThinking {
		if modelConfig.EnableThinking {
			response.Thinking = &reqStruct.ChatCompletionThinkingType{
				Type: "enabled",
			}
		} else {
			response.Thinking = &reqStruct.ChatCompletionThinkingType{
				Type: "disabled",
			}
		}
	}
	if modelConfig.ProviderSpecificConfig.EnableReasoningEffort && chatLn.ReasoningEffort != "" {
		reasoning := chatLn.ReasoningEffort
		response.ReasoningEffort = &reasoning
	}

	// 生成 messages
	responseDeltaList := list.New()
	exitFlag := false
	for offsetPage := range maxPage {
		var obj []structs.Messages
		if agentCode == "" {
			db.Where("`chat_id` = ? AND (`agent_id` = \"\" OR `agent_id` IS NULL)", chatID).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj)
		} else {
			db.Where("`chat_id` = ? AND `agent_id` = ?", chatID, agentCode).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj)
		}
		if len(obj) == 0 {
			break
		}
		for _, v := range obj {
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
					if renderAgentID == agentCode {
						if agentCode == "" {
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
					} else {
						thinkingWrap = v.ThinkingDelta
					}
					msg.Content = prompts.Render(prompts.DeltaWrapTemplate, struct {
						Thinking  string
						Delta     string
						ToolsCall string
					}{
						Thinking:  thinkingWrap,
						Delta:     v.Delta,
						ToolsCall: v.ToolCallingJSONString,
					})
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

	// 放置全局信息
	// 放置额外动态信息
	if addUserPrompt != "" {
		responseDeltaList.PushFront(reqStruct.Message{
			Role:    "user",
			Content: addUserPrompt,
		})
	}

	// 合并所有 system 消息
	var systemContent string

	// 1. global提示词 (GlobalTemplate)
	systemContent += prompts.Render(prompts.GlobalTemplate, struct {
		ModelName string
	}{
		ModelName: modelConfig.ModelName,
	}) + "\\n\\n"

	// 2. 用户设置 (GlobalPrompt)
	if config.GlobalConfig.Agent.GlobalPrompt != "" {
		systemContent += config.GlobalConfig.Agent.GlobalPrompt + "\\n\\n"
	}

	// 3. agent提示词
	if agentCode != "" {
		systemContent += agentCfg.AgentPrompt + "\\n\\n"
	} else {
		systemContent += prompts.DefaultAgent + "\\n\\n"
	}

	// 4. 工具使用指引
	systemContent += prompts.Tools + "\\n\\n"
	// 神秘上下文优化，但是好用
	systemContent += prompts.Tools + "\\n\\n"

	// 5. 工具列表
	systemContent += prompts.Render(prompts.ToolsWrapTemplate, struct {
		Tools string
	}{
		Tools: string(toolsLst),
	}) + "\\n\\n"

	// 6. 额外动态系统信息
	if addSystemPrompt != "" {
		systemContent += addSystemPrompt + "\\n\\n"
	}

	// 放置合并后的 system 消息
	responseDeltaList.PushFront(reqStruct.Message{
		Role:    "system",
		Content: systemContent,
	})

	// list 转 slice
	response.Messages = make([]reqStruct.Message, responseDeltaList.Len())
	for i, j := 0, responseDeltaList.Front(); j != nil; i, j = i+1, j.Next() {
		response.Messages[i] = j.Value.(reqStruct.Message)
	}
	return response, nil
}
