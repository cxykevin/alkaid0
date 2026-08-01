package build

import (
	"container/list"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/prompts"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// titleMaxToken 标题生成的最大 token 数（标题很短，小预算即可）
const titleMaxToken = 256

// Title 构建会话标题生成请求（首次生成：第一条用户请求 + 第一条 AI 响应）。
// 消息不足时返回 (nil, nil)，调用方应跳过。
func Title(chatID uint32, db *gorm.DB) (*reqStruct.ChatCompletionRequest, error) {
	var userMsg, agentMsg structs.Messages
	// 第一条用户请求：主线程消息（agent_id 为空或 NULL），排除空正文占位
	db.Where("`chat_id` = ? AND `type` = ? AND `delta` != '' AND (`agent_id` = '' OR `agent_id` IS NULL)", chatID, structs.MessagesRoleUser).
		Order("id ASC").Limit(1).Find(&userMsg)
	// 第一条 AI 响应：不过滤 agent_id（首轮委托子代理时回复带子代理 ID，也属于第一条响应）
	db.Where("`chat_id` = ? AND `type` = ? AND `delta` != ''", chatID, structs.MessagesRoleAgent).
		Order("id ASC").Limit(1).Find(&agentMsg)
	if userMsg.ID == 0 || agentMsg.ID == 0 {
		return nil, nil
	}
	return buildTitleRequest([]reqStruct.Message{
		{Role: "user", Content: userMsg.Delta},
		{Role: "assistant", Content: agentMsg.Delta},
	})
}

// TitleFull 构建会话标题生成请求（compress 重生成：完整对话）。
// 无有效消息时返回 (nil, nil)，调用方应跳过。
func TitleFull(chatID uint32, db *gorm.DB) (*reqStruct.ChatCompletionRequest, error) {
	responseDeltaList := list.New()
	for offsetPage := range maxPage {
		var obj []structs.Messages
		db.Where("`chat_id` = ? AND (`agent_id` = \"\" OR `agent_id` IS NULL)", chatID).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj)
		if len(obj) == 0 {
			break
		}
		for _, v := range obj {
			if v.Delta == "" {
				continue // 跳过无正文的占位消息（工具轮/被中断轮）
			}
			responseDeltaList.PushFront(reqStruct.Message{
				Role:    msgRole[v.Type],
				Content: v.Delta,
			})
		}
	}
	if responseDeltaList.Len() == 0 {
		return nil, nil
	}
	messages := make([]reqStruct.Message, 0, responseDeltaList.Len())
	for j := responseDeltaList.Front(); j != nil; j = j.Next() {
		messages = append(messages, j.Value.(reqStruct.Message))
	}
	return buildTitleRequest(messages)
}

// buildTitleRequest 组装标题生成请求：system 全局提示词 + 对话消息 + 标题指令
func buildTitleRequest(dialogMessages []reqStruct.Message) (*reqStruct.ChatCompletionRequest, error) {
	modelConfig, err := GetModelConfig(config.GlobalConfig.Agent.TitleModel)
	if err != nil {
		return nil, err
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
	maxTokenObj := titleMaxToken
	response.MaxTokens = &maxTokenObj

	// 生成 messages：system 全局提示词 + 对话消息 + 标题指令
	globalRendered, err := prompts.Render(prompts.GlobalTemplate, struct {
		ModelName string
	}{
		ModelName: modelConfig.ModelName,
	})
	if err != nil {
		return nil, err
	}
	messages := make([]reqStruct.Message, 0, len(dialogMessages)+2)
	messages = append(messages, reqStruct.Message{
		Role:    "system",
		Content: globalRendered,
	})
	messages = append(messages, dialogMessages...)
	messages = append(messages, reqStruct.Message{
		Role:    "user",
		Content: prompts.Title,
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
	return response, nil
}
