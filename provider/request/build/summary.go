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

// summaryWindowMessages 总结上下文纳入的消息条数上限（含工具轮：工具结果行占用窗口，
// 只是不产生正文，见下方 skipMsg）。
// 摘要按需截断即可、不参与前缀稳定性，因此与 maxReplayPage 的"只增不减"无关。
// 该值同时决定压缩边界能回溯多远：窗口扫不到的更旧消息既不被总结、也不再回放。
const summaryWindowMessages = 250

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
	// temperature=0 是合法的显式采样温度，仅 -1 表示"未设置"（此前 0 被一并忽略）
	if modelConfig.ProviderSpecificConfig.EnableTemperature && modelConfig.ModelTemperature != -1 {
		response.Temperature = &modelConfig.ModelTemperature
	}
	if modelConfig.ProviderSpecificConfig.EnableTopP && modelConfig.ModelTopP != -1 && modelConfig.ModelTopP != 0 {
		response.TopP = &modelConfig.ModelTopP
	}
	// 与主请求同一套规则：最大输出 token 数走 max_completion_tokens 并夹到区间内
	completionTokens := completionTokenLimit(modelConfig.MaxCompletionTokens)
	response.MaxCompletionTokens = &completionTokens

	// 生成 messages
	responseDeltaList := list.New()
	exitFlag := false
	// scanned 统计已纳入窗口的消息条数（含被 skipMsg 跳过的工具轮），windowReached 表示窗口已满。
	scanned := 0
	windowReached := false
	var lastMsgID uint64
	var totalMsgCount int64
	if agentID == "" {
		if err := structs.OnActiveBranch(db.Model(&structs.Messages{}), chatID).
			Where("`agent_id` = \"\" OR `agent_id` IS NULL").
			Count(&totalMsgCount).Error; err != nil {
			return 0, nil, err
		}
	} else {
		if err := structs.OnActiveBranch(db.Model(&structs.Messages{}), chatID).
			Where("`agent_id` = ?", agentID).
			Count(&totalMsgCount).Error; err != nil {
			return 0, nil, err
		}
	}

	// 窗口条数可能超过 maxPage*readPageSize（250 > 200），分页上限由窗口推导。
	windowPages := (summaryWindowMessages + readPageSize - 1) / readPageSize
	for offsetPage := range windowPages {
		var obj []structs.Messages
		if agentID == "" {
			if err := structs.OnActiveBranch(db, chatID).
				Where("`agent_id` = \"\" OR `agent_id` IS NULL").
				Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj).Error; err != nil {
				return 0, nil, err
			}
		} else {
			if err := structs.OnActiveBranch(db, chatID).
				Where("`agent_id` = ?", agentID).
				Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj).Error; err != nil {
				return 0, nil, err
			}
		}
		if len(obj) == 0 {
			break
		}
		for idx, v := range obj {
			// 窗口已满：不再向更旧的方向扫描（工具轮也计入窗口，只是在下方被跳过正文）。
			if scanned >= summaryWindowMessages {
				windowReached = true
				break
			}
			scanned++
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
					// 工具调用信息不进入总结：工具结果消息与带工具调用的 assistant 消息一律跳过。
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
			// 空的 assistant 行（取消遗留 / 历史脏数据）没有任何可总结内容，
			// 带上会让总结模型看到空回复，直接跳过。
			if v.Type == structs.MessagesRoleAgent && msg.Content == "" &&
				(msg.ReasoningContent == nil || *msg.ReasoningContent == "") {
				skipMsg = true
			}
			if skipMsg {
				continue
			}
			responseDeltaList.PushFront(msg)
			if exitFlag {
				break
			}
		}
		if exitFlag || windowReached {
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
