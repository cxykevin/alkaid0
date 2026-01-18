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
	"gorm.io/gorm"
)

const readPageSize = 20
const maxPage = 10
const maxToken = 8192

var msgRole = map[structs.MessagesRole]string{
	structs.MessagesRoleUser:  "user",
	structs.MessagesRoleAgent: "assistant",
	structs.MessagesRoleTool:  "user",
}

// RequestBody 构建请求
func RequestBody(chatID uint32, modelID int32, agentID string, toolsList *[]*parser.ToolsDefine, db *gorm.DB, addSystemPrompt string, addUserPrompt string) (*reqStruct.ChatCompletionRequest, error) {
	toolsLst, err := json.Marshal(*toolsList)
	if err != nil {
		return nil, err
	}

	modelConfig, err := GetModelConfig(modelID)
	if err != nil {
		return nil, err
	}

	var agentConfig *cfgStruct.AgentConfig = nil
	if agentID != "" {
		agentConfig, err = getAgentConfig(agentID)
		if err != nil {
			return nil, err
		}
	}

	response := &reqStruct.ChatCompletionRequest{}

	// 配置模型信息
	response.Model = modelConfig.ModelID
	response.Stream = true
	if modelConfig.ModelTemperature != -1 {
		response.Temperature = &modelConfig.ModelTemperature
	}
	if modelConfig.ModelTopP != -1 {
		response.TopP = &modelConfig.ModelTopP
	}
	var maxTokenObj int = maxToken
	response.MaxTokens = &maxTokenObj

	// 生成 messages
	responseDeltaList := list.New()
	exitFlag := false
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
	if addSystemPrompt != "" {
		responseDeltaList.PushFront(reqStruct.Message{
			Role:    "system",
			Content: addUserPrompt,
		})
	}
	// 放置工具列表
	responseDeltaList.PushFront(reqStruct.Message{
		Role: "system",
		Content: prompts.Render(prompts.ToolsWrapTemplate, struct {
			Tools string
		}{
			Tools: string(toolsLst),
		}),
	})
	// 放置工具使用指引
	responseDeltaList.PushFront(reqStruct.Message{
		Role:    "system",
		Content: prompts.Tools,
	})
	// 再放agent提示词
	if agentConfig != nil {
		responseDeltaList.PushFront(reqStruct.Message{
			Role:    "system",
			Content: agentConfig.AgentPrompt,
		})
	} else {
		responseDeltaList.PushFront(reqStruct.Message{
			Role:    "system",
			Content: prompts.DefaultAgent,
		})
	}
	// 再放用户设置
	responseDeltaList.PushFront(reqStruct.Message{
		Role:    "system",
		Content: config.GlobalConfig.Agent.GlobalPrompt,
	})
	// 再放global提示词
	responseDeltaList.PushFront(reqStruct.Message{
		Role: "system",
		Content: prompts.Render(prompts.GlobalTemplate, struct {
			ModelName string
		}{
			ModelName: modelConfig.ModelName,
		}),
	})

	// list 转 slice
	response.Messages = make([]reqStruct.Message, responseDeltaList.Len())
	for i, j := 0, responseDeltaList.Front(); j != nil; i, j = i+1, j.Next() {
		response.Messages[i] = j.Value.(reqStruct.Message)
	}
	return response, nil
}
