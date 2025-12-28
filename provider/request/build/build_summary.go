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
func Summary(chatID uint32, db *gorm.DB) (*reqStruct.ChatCompletionRequest, error) {

	modelConfig, err := getModelConfig(config.GlobalConfig.Agent.SummaryModel)
	if err != nil {
		return nil, err
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
		db.Where("`chat_id` = ?", chatID).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj)
		if len(obj) == 0 {
			break
		}
		for idx, v := range obj {
			if offsetPage == 0 && len(obj)-idx < summaryKeepNumber {
				continue
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
	return response, nil
}
