package build

import (
	"container/list"
	"encoding/json"
	"strings"

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
func RequestBody(chatID uint32, modelID int32, agentCode string, toolsList *[]*parser.ToolsDefine, db *gorm.DB, addSystemPrompt string, addUserPrompt string, agentCfg cfgStruct.AgentConfig, chatLn *storageStructs.Chats) (*reqStruct.ChatCompletionRequest, error) {
	toolsLst, err := json.Marshal(*toolsList)
	if err != nil {
		return nil, err
	}

	modelConfig, err := GetModelConfig(modelID)
	if err != nil {
		return nil, err
	}

	response := &reqStruct.ChatCompletionRequest{}
	nativeMode := modelConfig.EnableToolCalling

	// 配置模型信息
	response.Model = modelConfig.ModelID
	response.Stream = true
	if nativeMode && toolsList != nil {
		// 原生模式：工具定义通过 API tools 参数声明（而非注入提示词）
		tools := make([]reqStruct.Tool, 0, len(*toolsList))
		for _, td := range *toolsList {
			if td == nil {
				continue
			}
			schemaRaw, err := json.Marshal(ToolParametersToJSONSchema(td.Parameters))
			if err != nil {
				return nil, err
			}
			tools = append(tools, reqStruct.Tool{
				Type: "function",
				Function: reqStruct.ToolFunction{
					Name:        td.Name,
					Description: td.Description,
					Parameters:  schemaRaw,
				},
			})
		}
		response.Tools = tools
		response.ToolChoice = "auto"
	}
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
	if modelConfig.ProviderSpecificConfig.EnableReasoningEffort {
		reasoning := chatLn.ReasoningEffort
		if reasoning == "" {
			reasoning = "unset"
		}
		if reasoning != "unset" {
			response.ReasoningEffort = &reasoning
		}
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
				rendered, err := prompts.Render(prompts.SummaryWrapTemplate, struct {
					Summary string
				}{Summary: v.Summary})
				if err != nil {
					return nil, err
				}
				msg.Content = rendered
				exitFlag = true
			} else {
				if nativeMode && v.Type == structs.MessagesRoleAgent {
					// 原生模式：assistant 历史消息用原来的文本拼法回放——工具调用以 <tools> 段
					// 拼回 content（不引入原生 tool_calls/tool_call_id）；工具返回也走原来的
					// <tools_return> 文本段（见下方 MessagesRoleTool 分支）。仅当前轮请求用
					// 原生 tools 参数声明、响应用原生 tool_calls 解析。
					msg.Role = reqStruct.RoleAssistant
					thinkingWrap := ""
					if modelConfig.EnableThinking && v.ThinkingDelta != "" {
						thinkingString := v.ThinkingDelta
						msg.ReasoningContent = &thinkingString
					} else if !modelConfig.EnableThinking {
						thinkingWrap = v.ThinkingDelta
					}
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
						return nil, err
					}
					msg.Content = deltaRendered
				} else if v.Type == structs.MessagesRoleUser {
					rendered, err := prompts.Render(prompts.UserWrapTemplate, struct {
						Prompt string
						Refers structs.MessagesReferList
					}{
						Prompt: v.Delta,
						Refers: v.Refers,
					})
					if err != nil {
						return nil, err
					}
					msg.Content = rendered
				} else if v.Type == structs.MessagesRoleTool {
					toolRendered, err := prompts.Render(prompts.ToolResponseWrapTemplate, struct {
						Prompt string
					}{
						Prompt: v.Delta,
					})
					if err != nil {
						return nil, err
					}
					msg.Content = toolRendered
				} else if v.Type == structs.MessagesRoleCommunicate {
					renderAgentID := ""
					if v.AgentID != nil {
						renderAgentID = *v.AgentID
					}
					if renderAgentID == agentCode {
						if agentCode == "" {
							agentRendered, err := prompts.Render(prompts.AgentWrapTemplate, struct {
								Prompt string
							}{
								Prompt: v.Delta,
							})
							if err != nil {
								return nil, err
							}
							msg.Content = agentRendered
						} else {
							subAgentRendered, err := prompts.Render(prompts.SubagentWrapTemplate, struct {
								Prompt string
							}{
								Prompt: v.Delta,
							})
							if err != nil {
								return nil, err
							}
							msg.Content = subAgentRendered
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
						return nil, err
					}
					msg.Content = deltaRendered
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
	globalRendered, err := prompts.Render(prompts.GlobalTemplate, struct {
		ModelName string
	}{
		ModelName: modelConfig.ModelName,
	})
	if err != nil {
		return nil, err
	}
	systemContent += globalRendered + "\n\n"

	// 2. 用户设置 (GlobalPrompt)
	if config.GlobalConfig.Agent.GlobalPrompt != "" {
		systemContent += config.GlobalConfig.Agent.GlobalPrompt + "\n\n"
	}

	// 3. agent提示词
	if agentCode != "" {
		systemContent += agentCfg.AgentPrompt + "\n\n"
	} else {
		systemContent += prompts.DefaultAgent + "\n\n"
	}

	// 4. 工具使用指引（模式相关）：
	//    提示词模式：prompts.Tools（永远拼接两次，第二次由增强开关决定）
	//    原生模式：prompts.ToolNative + 反向增强段（ToolEnhanceNative，警告 <tools> 标签）
	if nativeMode {
		systemContent += prompts.ToolNative + "\n\n"
		// 反幻觉增强段暂时禁用：调试原生 tool_calls 格式时保持提示词纯净，
		// 待工具调用格式稳定后再按模型恢复。恢复时取消下面两行注释。
		// if enhance := NativeToolPromptEnhanceBlock(modelConfig); enhance != "" {
		// 	systemContent += enhance + "\n\n"
		// }
	} else {
		systemContent += prompts.Tools + "\n\n"
		// 4b. 反幻觉增强段（ProviderSpecificConfig.ToolPromptEnhance 控制；
		// auto 下 GPT/Claude 系模型 id 命中免增强名单时回退为基础版）
		if enhance := ToolPromptEnhanceBlock(modelConfig); enhance != "" {
			systemContent += enhance + "\n\n"
		} else {
			systemContent += prompts.Tools + "\n\n"
		}
	}

	// 5. 工具列表（仅提示词模式注入 <tools_input>；原生模式工具定义走 API tools 参数）
	if !nativeMode {
		toolsRendered, err := prompts.Render(prompts.ToolsWrapTemplate, struct {
			Tools string
		}{
			Tools: string(toolsLst),
		})
		if err != nil {
			return nil, err
		}
		systemContent += toolsRendered + "\n\n"
	}

	// 6. 自动审批规则说明 — 告知 AI 哪些工具会不经确认直接执行
	autoApproveRules := getEffectiveAutoApprove(agentCfg)
	autoRejectRules := getEffectiveAutoReject(agentCfg)
	var approvalInfo string
	if autoApproveRules != "" || autoRejectRules != "" {
		approvalInfo += "\n[Auto Approval Rules]\n"
		approvalInfo += "The following rules determine whether tool calls are automatically approved or rejected.\n"
		approvalInfo += "Tools matching Auto-Approve rules will execute without waiting for user confirmation.\n"
		approvalInfo += "Tools matching Auto-Reject rules are automatically blocked and will not execute.\n"
		if autoApproveRules != "" {
			approvalInfo += "Auto-Approve: " + autoApproveRules + "\n"
		}
		if autoRejectRules != "" {
			approvalInfo += "Auto-Reject: " + autoRejectRules + "\n"
		}
		approvalInfo += "Plan your tool usage accordingly — avoid calling tools that will be rejected.\n"
		systemContent += approvalInfo + "\n"
	}

	// 7. [path:@temp/...] marker explanation
	if !config.GlobalConfig.Agent.DisablePromptPreprocess {
		systemContent += "\n[Prompt Preprocessing]\n"
		systemContent += "When user input contains large code blocks or logs, they are extracted and saved to temporary files.\n"
		systemContent += "The user message will show [path:@temp/prompt/code-...] (for code) or [path:@temp/prompt/log-...] (for log) instead.\n"
		systemContent += "Use `trace` tool with this path to read the full content if needed.\n"
		systemContent += "Make sure to use the full path including @temp/prompt/ prefix.\n\n"
	}

	// 8. extra dynamic system prompts
	if addSystemPrompt != "" {
		systemContent += addSystemPrompt + "\n\n"
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

// getEffectiveAutoApprove 获取用户配置的 AutoApprove 规则（不含内置规则合并）
func getEffectiveAutoApprove(agentCfg cfgStruct.AgentConfig) string {
	r := strings.TrimSpace(agentCfg.AutoApprove)
	if r == "" {
		r = strings.TrimSpace(config.GlobalConfig.Agent.DefaultAutoApprove)
	}
	return r
}

// getEffectiveAutoReject 获取用户配置的 AutoReject 规则（不含内置规则合并）
func getEffectiveAutoReject(agentCfg cfgStruct.AgentConfig) string {
	r := strings.TrimSpace(agentCfg.AutoReject)
	if r == "" {
		r = strings.TrimSpace(config.GlobalConfig.Agent.DefaultAutoReject)
	}
	return r
}
