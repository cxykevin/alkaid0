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

// titleWindowMessages 标题重生成（TitleFull）纳入的对话消息条数上限。
// 工具调用（工具结果行）既不计入该窗口，也不进标题上下文：它们对标题没有信息量。
const titleWindowMessages = 50

// Title 构建会话标题生成请求（首次生成：第一条用户请求 + 第一条 AI 响应）。
// 消息不足时返回 (nil, nil)，调用方应跳过。
func Title(chatID uint32, db *gorm.DB) (*reqStruct.ChatCompletionRequest, error) {
	var userMsg, agentMsg structs.Messages
	// 第一条用户请求：主线程消息（agent_id 为空或 NULL），排除空正文占位
	if err := structs.OnActiveBranch(db, chatID).
		Where("`type` = ? AND `delta` != '' AND (`agent_id` = '' OR `agent_id` IS NULL)", structs.MessagesRoleUser).
		Order("id ASC").Limit(1).Find(&userMsg).Error; err != nil {
		return nil, err
	}
	// 第一条 AI 响应：不过滤 agent_id（首轮委托子代理时回复带子代理 ID，也属于第一条响应）
	if err := structs.OnActiveBranch(db, chatID).
		Where("`type` = ? AND `delta` != ''", structs.MessagesRoleAgent).
		Order("id ASC").Limit(1).Find(&agentMsg).Error; err != nil {
		return nil, err
	}
	if userMsg.ID == 0 || agentMsg.ID == 0 {
		return nil, nil
	}
	return buildTitleRequest([]reqStruct.Message{
		{Role: "user", Content: userMsg.Delta},
		{Role: "assistant", Content: agentMsg.Delta},
	})
}

// TitleFull 构建会话标题生成请求（compress 重生成：最近 titleWindowMessages 条对话消息）。
// 无有效消息时返回 (nil, nil)，调用方应跳过。
func TitleFull(chatID uint32, db *gorm.DB) (*reqStruct.ChatCompletionRequest, error) {
	responseDeltaList := list.New()
	// collected 统计已纳入窗口的对话消息条数；工具结果行不计数（见下）。
	collected := 0
	windowReached := false
	for offsetPage := range maxPage {
		var obj []structs.Messages
		if err := structs.OnActiveBranch(db, chatID).
			Where("`agent_id` = \"\" OR `agent_id` IS NULL").
			Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj).Error; err != nil {
			return nil, err
		}
		if len(obj) == 0 {
			break
		}
		for _, v := range obj {
			if collected >= titleWindowMessages {
				windowReached = true
				break
			}
			if v.Type == structs.MessagesRoleTool {
				continue // 工具调用结果：不算一条，也不进标题上下文
			}
			if v.Delta == "" {
				continue // 跳过无正文的占位消息（工具轮/被中断轮）
			}
			responseDeltaList.PushFront(reqStruct.Message{
				Role:    msgRole[v.Type],
				Content: v.Delta,
			})
			collected++
		}
		if windowReached {
			break
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

// buildTitleRequest 组装标题生成请求：标题专用 system 提示词 + 对话消息 + 标题指令
func buildTitleRequest(dialogMessages []reqStruct.Message) (*reqStruct.ChatCompletionRequest, error) {
	modelConfig, err := GetModelConfig(config.GlobalConfig.Agent.TitleModel)
	if err != nil {
		return nil, err
	}

	response := &reqStruct.ChatCompletionRequest{}

	// 配置模型信息
	response.Model = modelConfig.ModelID
	response.Stream = true
	// temperature=0 是合法的显式采样温度，仅 -1 表示"未设置"（此前 0 被一并忽略）
	if modelConfig.ProviderSpecificConfig.EnableTemperature && modelConfig.ModelTemperature != -1 {
		response.Temperature = &modelConfig.ModelTemperature
	}
	if modelConfig.ProviderSpecificConfig.EnableTopP && modelConfig.ModelTopP != -1 && modelConfig.ModelTopP != 0 {
		response.TopP = &modelConfig.ModelTopP
	}
	// 标题期望的预算很小，但网关对 max_completion_tokens 有下限要求，
	// 统一由 completionTokenLimit 夹到 [4096, 32768]。
	titleTokens := completionTokenLimit(titleMaxToken)
	response.MaxCompletionTokens = &titleTokens

	// 生成 messages：标题专用 system 提示词 + 对话消息 + 标题指令。
	// 注意：不能复用 GlobalTemplate（global.md，面向软件工程师的编码提示词）——
	// 其 "Think in English" 会与标题语言匹配规则冲突、git 提交/ReACT 等无关指令
	// 会污染输出并浪费 token。改用精简的标题专用 system（title_system.md）。
	titleSystemRendered, err := prompts.Render(prompts.TitleSystemTemplate, struct{}{})
	if err != nil {
		return nil, err
	}
	messages := make([]reqStruct.Message, 0, len(dialogMessages)+2)
	messages = append(messages, reqStruct.Message{
		Role:    "system",
		Content: titleSystemRendered,
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
