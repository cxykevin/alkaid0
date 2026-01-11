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
	var lastMsgID uint64
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
			if offsetPage == 0 && idx < keepNum {
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
				if v.ThinkingDelta != "" {
					if modelConfig.EnableThinking {
						thinkingString := v.ThinkingDelta
						msg.ReasoningContent = &thinkingString
						msg.Content = v.Delta
					} else {
						msg.Content = prompts.Render(prompts.ThinkingWrapTemplate, struct {
							Thinking string
						}{Thinking: v.ThinkingDelta}) + v.Delta
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

	// 放summary提示词
	responseDeltaList.PushFront(reqStruct.Message{
		Role:    "system",
		Content: prompts.Summary,
	})

	// list 转 slice
	response.Messages = make([]reqStruct.Message, responseDeltaList.Len())
	for i, j := 0, responseDeltaList.Front(); j != nil; i, j = i+1, j.Next() {
		response.Messages[i] = j.Value.(reqStruct.Message)
	}
	return lastMsgID, response, nil
}
