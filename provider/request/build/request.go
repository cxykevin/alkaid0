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

// toolCallTerminatedMsg 工具调用被强行终止（无对应结果消息）时补发的占位结果内容。
// 保证 assistant 的每个 tool_call_id 都有 role:"tool" 响应，满足 OpenAI 兼容 API 校验，
// 同时让模型知道该调用未执行。
const toolCallTerminatedMsg = "[Tool call terminated] This call was cancelled before execution and did not run."

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

	// 预扫描（原生模式）：收集回放范围内所有 assistant 工具调用 id（toolCallIDs，
	// 用于工具结果配对）、所有工具结果 id（resultIDs，用于判断哪些调用结果缺失）与
	// 失败调用 id（failedResultIDs，其真实参数在回放时不再省略，供模型自我修正）。
	// 回放是倒序查询 + PushFront，结果消息先于其 assistant 被处理，无法边扫边配，
	// 因此在主循环前先倒序分页扫描一次。id 全局唯一、结果紧跟调用，不会误配。
	// 与主循环一致：遇到 summary（历史截断点）即停止。
	var toolCallIDs, resultIDs, failedResultIDs map[string]struct{}
	if nativeMode {
		toolCallIDs = make(map[string]struct{})
		resultIDs = make(map[string]struct{})
		failedResultIDs = make(map[string]struct{})
	scan:
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
				if v.Summary != "" {
					break scan
				}
				if v.Type == structs.MessagesRoleTool {
					results, err := parseStoredToolResults(v.Delta)
					if err == nil {
						for _, r := range results {
							if r.ID != "" {
								resultIDs[r.ID] = struct{}{}
								if isFailedToolResult(r.Return) {
									failedResultIDs[r.ID] = struct{}{}
								}
							}
						}
					}
				} else if v.Type == structs.MessagesRoleAgent && v.ToolCallingJSONString != "" {
					calls, err := parseStoredToolCalls(v.ToolCallingJSONString, nil)
					if err == nil {
						for _, c := range calls {
							if c.ID != "" {
								toolCallIDs[c.ID] = struct{}{}
							}
						}
					}
				}
			}
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
					// 原生模式：assistant 历史消息回放原生 tool_calls —— content 保留文本 Delta，
					// 工具调用解析 ToolCallingJSONString 为 msg.ToolCalls（tool_call_id + function.name/arguments），
					// 与当前轮请求的 tools 参数/响应解析同一种格式，不再出现 <tools> 文本段。
					msg.Role = reqStruct.RoleAssistant
					thinkingWrap := ""
					if modelConfig.EnableThinking && v.ThinkingDelta != "" {
						thinkingString := v.ThinkingDelta
						msg.ReasoningContent = &thinkingString
					} else if !modelConfig.EnableThinking {
						thinkingWrap = v.ThinkingDelta
					}
					msg.Content = v.Delta
					if thinkingWrap != "" {
						msg.Content = "<think>\n" + thinkingWrap + "\n</think>\n" + v.Delta
					}
					if v.ToolCallingJSONString != "" {
						toolCalls, err := parseStoredToolCalls(v.ToolCallingJSONString, failedResultIDs)
						if err == nil && len(toolCalls) > 0 {
							msg.ToolCalls = toolCalls
							// 为被终止（无对应结果消息）的调用补发占位 role:"tool" 消息，
							// 保证 assistant 的每个 tool_call_id 都有响应，满足 API 校验
							// "assistant with tool_calls must be followed by tool messages"。
							// 倒序 PushFront：占位按 toolCalls 顺序排在 assistant 之后。
							for i := len(toolCalls) - 1; i >= 0; i-- {
								c := toolCalls[i]
								if c.ID == "" {
									continue
								}
								if _, ok := resultIDs[c.ID]; !ok {
									responseDeltaList.PushFront(reqStruct.Message{
										Role:       reqStruct.RoleTool,
										Content:    toolCallTerminatedMsg,
										ToolCallID: c.ID,
									})
								}
							}
						}
						// 解析失败容错为普通文本消息（Content 已保留），不中断回放
					}
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
				} else if !nativeMode && v.Type == structs.MessagesRoleTool {
					// 提示词模式：工具结果以 <tools_return> 文本段回放
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
			if nativeMode && v.Type == structs.MessagesRoleTool {
				// 原生模式：工具结果按 id 拆分为多条 role:"tool" 消息，严格配对——
				// 结果 id 必须命中预扫描的 assistant 工具调用集合（丢弃孤立的幽灵结果）。
				// 被终止调用的占位结果由 assistant 分支补齐。
				// PushFront 正序：多条需倒序 push。
				results, err := parseStoredToolResults(v.Delta)
				if err == nil && len(results) > 0 {
					for i := len(results) - 1; i >= 0; i-- {
						r := results[i]
						if _, ok := toolCallIDs[r.ID]; !ok {
							continue
						}
						responseDeltaList.PushFront(reqStruct.Message{
							Role:       reqStruct.RoleTool,
							Content:    r.Return,
							ToolCallID: r.ID,
						})
					}
				}
				// 结果消息已按 id 拆分推送（或解析失败/全部丢弃则不推送），跳过统一 push
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

// storedToolCall 存储层工具调用项（tool_calling_json_string 内部格式 [{"name","id","parameters"}]）。
// 存储层为标准 encoding/json 序列化（nativeAcc.Origin()），此处直接解析，避免依赖 request 包（循环依赖）。
// id/name 用于回放调用结构；Parameters 保留真实参数，供失败调用的历史回放使用。
type storedToolCall struct {
	Name       string          `json:"name"`
	ID         string          `json:"id"`
	Parameters json.RawMessage `json:"parameters"`
}

// parseStoredToolCalls 解析存储层工具调用 JSON 为原生 tool_calls 消息。
// 返回 nil（空 payload/解析失败）时表示无工具调用。
// 历史回放默认不携带具体参数内容：arguments 统一以 "..." 占位，只保留调用结构
// （id/type/function.name），避免把完整命令与参数重复发给模型。
// 例外：failedIDs 中命中的调用（对应工具结果含 success:false / error，即调用失败）
// 回放真实参数（Parameters 原样填入 arguments），让模型看到自己上次传错的参数名以自我修正。
func parseStoredToolCalls(payload string, failedIDs map[string]struct{}) ([]reqStruct.StreamToolCall, error) {
	if strings.TrimSpace(payload) == "" {
		return nil, nil
	}
	var items []storedToolCall
	if err := json.Unmarshal([]byte(payload), &items); err != nil {
		return nil, err
	}
	calls := make([]reqStruct.StreamToolCall, 0, len(items))
	for _, it := range items {
		args := "..."
		if failedIDs != nil && len(it.Parameters) > 0 {
			if _, failed := failedIDs[it.ID]; failed {
				args = string(it.Parameters)
			}
		}
		calls = append(calls, reqStruct.StreamToolCall{
			ID:   it.ID,
			Type: "function",
			Function: &reqStruct.StreamToolCallFunc{
				Name:      it.Name,
				Arguments: args,
			},
		})
	}
	return calls, nil
}

// isFailedToolResult 判定工具结果是否表示调用失败。
// 内置工具失败时返回 map 序列化为含 "success":false 或 "error" 键的 JSON。
// 判定规则：success 键优先——存在且为 false 即失败；为 true 时不判失败（即使带 error 键，
// 防误判）；success 键缺失时回退检查 error 键是否存在。返回空/非 JSON 对象一律视为成功。
func isFailedToolResult(returnJSON string) bool {
	if strings.TrimSpace(returnJSON) == "" {
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(returnJSON), &obj); err != nil {
		return false
	}
	if success, ok := obj["success"]; ok {
		if b, isBool := success.(bool); isBool {
			return !b
		}
	}
	_, hasError := obj["error"]
	return hasError
}

// storedToolResult 存储层工具结果项（MessagesRoleTool.Delta 内部格式 [{"name","id","return"}]）。
type storedToolResult struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Return string `json:"return"`
}

// parseStoredToolResults 解析存储层工具结果 JSON。空 payload/解析失败返回空切片。
func parseStoredToolResults(payload string) ([]storedToolResult, error) {
	if strings.TrimSpace(payload) == "" {
		return nil, nil
	}
	var items []storedToolResult
	if err := json.Unmarshal([]byte(payload), &items); err != nil {
		return nil, err
	}
	return items, nil
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
