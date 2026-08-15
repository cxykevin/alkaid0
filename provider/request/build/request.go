package build

import (
	"container/list"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

const readPageSize = 20
const maxPage = 10
const maxToken = 16384

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
	modelConfig, err := GetModelConfig(modelID)
	if err != nil {
		return nil, err
	}

	response := &reqStruct.ChatCompletionRequest{}

	// 配置模型信息
	response.Model = modelConfig.ModelID
	response.Stream = true
	if toolsList != nil {
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

	// 预扫描：收集回放范围内
	//   toolCallIDs    — 所有 assistant 工具调用 id（用于工具结果配对，丢弃孤立幽灵结果）
	//   resultIDs      — 所有工具结果 id（用于判断哪些调用结果缺失、需补终止占位）
	// 回放是倒序查询 + PushFront，结果消息先于其 assistant 被处理，无法边扫边配，
	// 因此在主循环前先倒序分页扫描一次。截断点（Summary）之后的所有工具调用轮次
	// 全部完整回放 tool_calls 结构——同一条消息的渲染不再随轮次推移变化，
	// 前缀缓存才不被破坏。id 全局唯一、结果紧跟调用，不会误配。
	// 与主循环一致：遇到 summary（历史截断点）即停止。
	var toolCallIDs, resultIDs map[string]struct{}
	toolCallIDs = make(map[string]struct{})
	resultIDs = make(map[string]struct{})
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
						}
					}
				}
			} else if v.Type == structs.MessagesRoleAgent && v.ToolCallingJSONString != "" {
				calls, err := parseStoredToolCalls(v.ToolCallingJSONString)
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

	// 生成 messages
	responseDeltaList := list.New()
	// 记录每条 DB 消息记录 push 后的链表元素，供事件内容块按锚点插入（见 insertEventContentBlocks）。
	dbIDToElement := make(map[uint64]*list.Element)
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
			skipMsg := false
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
				if v.Type == structs.MessagesRoleAgent {
					// 原生模式：assistant 历史消息回放原生 tool_calls —— content 保留文本 Delta，
					// 工具调用解析 ToolCallingJSONString 为 msg.ToolCalls（tool_call_id + function.name/arguments），
					// 与当前轮请求的 tools 参数/响应解析同一种格式，不再出现 <tools> 文本段。
					msg.Role = reqStruct.RoleAssistant
					thinkingWrap := ""
					if modelConfig.EnableThinking {
						// thinking 模式：历史中一旦出现工具调用，此后每条 assistant 消息都必须携带
						// reasoning_content 字段（OpenAI 兼容）/ content[].thinking 块（Anthropic 兼容），
						// 空内容也须保留字段占位（空串即可通过 DeepSeek 校验），否则转换代理端
						// 报 400 "The content[].thinking in the thinking mode must be passed back to the API"。
						thinkingString := v.ThinkingDelta
						msg.ReasoningContent = &thinkingString
					} else if v.ThinkingDelta != "" {
						thinkingWrap = v.ThinkingDelta
					}
					msg.Content = v.Delta
					if thinkingWrap != "" {
						msg.Content = "<think>\n" + thinkingWrap + "\n</think>\n" + v.Delta
					}
					if v.ToolCallingJSONString != "" {
						toolCalls, err := parseStoredToolCalls(v.ToolCallingJSONString)
						if err == nil && len(toolCalls) > 0 {
							// 截断点之后的所有工具调用轮次均完整回放（带 tool_calls + 结果/终止占位），
							// 同一条消息渲染确定，前缀缓存才不被破坏。
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
					// 降级后无文本内容（纯工具调用轮次）：跳过空 assistant 消息。
					// thinking 模式下 ThinkingDelta 为空时仅保留空 reasoning_content 占位，
					// 同样跳过；有真实思考内容（非空）的消息即使无正文也保留。
					if len(msg.ToolCalls) == 0 && msg.Content == "" && (msg.ReasoningContent == nil || *msg.ReasoningContent == "") {
						skipMsg = true
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
			if v.Type == structs.MessagesRoleTool {
				// 原生模式：工具结果按 id 拆分为多条 role:"tool" 消息，严格配对——
				// 结果 id 必须命中全部回放轮次的 assistant 工具调用集合（丢弃孤立的
				// 幽灵结果）。被终止调用的占位结果由 assistant 分支补齐。
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
			if skipMsg {
				continue
			}
			dbIDToElement[v.ID] = responseDeltaList.PushFront(msg)
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
	// trace/@task 内容块按最近 read/edit 事件插入历史；不可锚定的事件内容作为顶部 fallback。
	var fallbackTop string
	if chatLn.TemporyDataOfSession != nil {
		if em, ok := chatLn.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent); ok && len(em) > 0 {
			prevEm, _ := chatLn.TemporyDataOfSession[structs.TempKeyTracePrevEvents].(map[string]*structs.TraceEvent)
			diffPlans, _ := chatLn.TemporyDataOfSession[structs.TempKeyTraceDiffPlan].(map[string]trace.DiffPlan)
			fallbackTop = insertEventContentBlocks(responseDeltaList, dbIDToElement, em, prevEm, diffPlans, chatLn)
		}
	}
	if addUserPrompt != "" || fallbackTop != "" {
		el := responseDeltaList.PushFront(reqStruct.Message{
			Role:    "user",
			Content: addUserPrompt,
		})
		if fallbackTop != "" {
			responseDeltaList.InsertAfter(reqStruct.Message{
				Role:    "user",
				Content: fallbackTop,
			}, el)
		}
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

	// 4. 原生工具使用指引
	systemContent += prompts.ToolNative + "\n\n"

	// 5. 工具列表（工具定义走 API tools 参数）

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
		systemContent += "Use `read` tool with this path to read the full content if needed.\n"
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

// maxReplayArgRunes 历史回放工具调用参数的最大字符数（rune）。
// 超过阈值的参数统一截断为 "..."，防 edit 等大参数（如完整文件内容）导致上下文膨胀。
const maxReplayArgRunes = 100

// parseStoredToolCalls 解析存储层工具调用 JSON 为原生 tool_calls 消息。
// 空参数使用兼容占位符；非空参数按长度限制回放。
func parseStoredToolCalls(payload string) ([]reqStruct.StreamToolCall, error) {
	if strings.TrimSpace(payload) == "" {
		return nil, nil
	}
	var items []storedToolCall
	if err := json.Unmarshal([]byte(payload), &items); err != nil {
		return nil, err
	}
	calls := make([]reqStruct.StreamToolCall, 0, len(items))
	for _, it := range items {
		args := "(omit successed tool call arguments)"
		if len(it.Parameters) > 0 {
			if utf8.RuneCount(it.Parameters) > maxReplayArgRunes {
				args = "..."
			} else {
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

// DetectTraceEvents 从新到旧扫描消息历史，为每个被 read/edit 交互过的路径记录最近一次事件。
// 结果写入 session.TemporyDataOfSession[TempKeyTraceEvents]（map[string]*structs.TraceEvent）。
// 从新到旧扫描，先扫到的即最近事件，天然满足"只留最新"。
// CollectTracePathsAfter 收集 summary 边界之后仍发生过 read/edit 的 trace 路径。
// afterMsgID 是已写入 Summary 的消息 ID；只扫描 ID 更大的消息，避免把已压缩历史
// 中的工具调用重新带回当前上下文。@task 是虚拟任务对象，不属于 Traces 表。
func CollectTracePathsAfter(db *gorm.DB, chatID uint32, agentID string, afterMsgID uint64) (map[string]struct{}, error) {
	paths := make(map[string]struct{})
	var messages []structs.Messages
	query := db.Where("chat_id = ? AND id > ?", chatID, afterMsgID)
	if agentID == "" {
		query = query.Where("(agent_id = '' OR agent_id IS NULL)")
	} else {
		query = query.Where("agent_id = ?", agentID)
	}
	if err := query.Order("id ASC").Find(&messages).Error; err != nil {
		return nil, err
	}
	for _, message := range messages {
		if message.Type != structs.MessagesRoleAgent || message.ToolCallingJSONString == "" {
			continue
		}
		var calls []storedToolCall
		if err := json.Unmarshal([]byte(message.ToolCallingJSONString), &calls); err != nil {
			continue
		}
		for _, call := range calls {
			if call.Name != "read" && call.Name != "edit" {
				continue
			}
			path := toolCallPath(call)
			if path != "" && path != "@task" {
				paths[path] = struct{}{}
			}
		}
	}
	return paths, nil
}

func DetectTraceEvents(db *gorm.DB, session *structs.Chats, agentCode string) error {
	eventMap := make(map[string]*structs.TraceEvent)
	prevMap := make(map[string]*structs.TraceEvent)
	recentTurns := 0
scan:
	for offsetPage := range maxPage {
		var obj []structs.Messages
		if agentCode == "" {
			db.Where("`chat_id` = ? AND (`agent_id` = \"\" OR `agent_id` IS NULL)", session.ID).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj)
		} else {
			db.Where("`chat_id` = ? AND `agent_id` = ?", session.ID, agentCode).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj)
		}
		if len(obj) == 0 {
			break
		}
		for _, v := range obj {
			if v.Summary != "" {
				break scan
			}
			if v.Type != structs.MessagesRoleAgent || v.ToolCallingJSONString == "" {
				continue
			}
			var items []storedToolCall
			if err := json.Unmarshal([]byte(v.ToolCallingJSONString), &items); err != nil || len(items) == 0 {
				continue
			}
			inRecent := recentTurns < 5
			recentTurns++
			for _, c := range items {
				if c.Name != "read" && c.Name != "edit" {
					continue
				}
				path := toolCallPath(c)
				if path == "" {
					continue
				}
				ev := &structs.TraceEvent{
					MsgID:      v.ID,
					ToolCallID: c.ID,
					IsEdit:     c.Name == "edit",
					IsTask:     path == "@task",
					InRecent:   inRecent,
				}
				if _, exists := eventMap[path]; exists {
					// 已记录最新事件，此处为更旧的事件：保留最早一条，作为方案2 旧块的固定锚点。
					// 从新到旧扫描，每次覆盖 prevMap，最后赋值即该 path 最早 read/edit 事件。
					// 旧块锚定「最早事件」位置，连续编辑时锚点不随次新事件漂移，前缀缓存才不被破坏
					// （此前锚定次新事件，连续 edit 每轮旧块位置前移、上轮 diff 块被同位置新块替换而断裂）。
					prevMap[path] = ev
					continue
				}
				eventMap[path] = ev
			}
		}
	}
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	session.TemporyDataOfSession[structs.TempKeyTraceEvents] = eventMap
	session.TemporyDataOfSession[structs.TempKeyTracePrevEvents] = prevMap
	return nil
}

// toolCallPath 从存储层工具调用参数中提取 path。
// 不能复用 parseStoredToolCalls：其回放时参数可能被截断为 "..."（超 maxReplayArgRunes 字符），
// 此处需要完整 path。
func toolCallPath(c storedToolCall) string {
	if len(c.Parameters) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(c.Parameters, &args); err != nil {
		return ""
	}
	p, _ := args["path"].(string)
	return p
}

// eventInsertGroup 同一事件（一条 assistant 消息）内多个文件的内容块合并为一条 user 消息。
type eventInsertGroup struct {
	anchor *list.Element
	paths  []string
}

// insertEventContentBlocks 按事件把 trace/@task 内容块插入历史（紧跟最近 read/edit 事件之后），
// 返回不可锚定事件的顶部 fallback 内容（独立 user 消息，插在 addUserPrompt 之后）。
// 内容块在 PreHook 阶段已渲染并暂存于 chatLn.TemporyDataOfSession，此处只做拼装插入，不重复读盘。
func insertEventContentBlocks(l *list.List, dbIDToElement map[uint64]*list.Element,
	eventMap, prevMap map[string]*structs.TraceEvent, diffPlans map[string]trace.DiffPlan, chatLn *structs.Chats) string {

	fileBlocks, _ := chatLn.TemporyDataOfSession[structs.TempKeyTraceFileBlocks].(map[string]trace.FileBlock)
	taskBlock, _ := chatLn.TemporyDataOfSession[structs.TempKeyTaskEventBlock].(string)

	groups := make(map[uint64]*eventInsertGroup)
	fallbackPaths := make([]string, 0)
	for path, ev := range eventMap {
		// 方案2：旧块（最早锚点，内容=LastContent 存档、字节稳定命中前缀）+ diff 块（最新锚点）都可锚定时，跳过常规最新块插入
		if plan, ok := diffPlans[path]; ok && plan.Keep {
			if prev, ok := prevMap[path]; ok {
				prevAnchor := findEventAnchor(l, dbIDToElement, prev)
				diffAnchor := findEventAnchor(l, dbIDToElement, ev)
				// 锚点都可用且软条件（含 betweenTok 连锁成本）满足 → 方案2
				if prevAnchor != nil && diffAnchor != nil && trace.KeepDiffPlan(plan, betweenTokens(prevAnchor, diffAnchor)) {
					if oldContent, err := trace.RenderTraceBlock([]trace.FileBlock{plan.OldBlock}); err == nil && oldContent != "" {
						l.InsertAfter(reqStruct.Message{Role: "user", Content: oldContent}, prevAnchor)
					}
					if diffContent, err := trace.RenderTraceBlock([]trace.FileBlock{plan.DiffBlock}); err == nil && diffContent != "" {
						l.InsertAfter(reqStruct.Message{Role: "user", Content: diffContent}, diffAnchor)
					}
					continue
				}
				// 锚点不可用或软条件不满足 → 退化为方案1
			}
		}
		if !ev.IsTask {
			if _, ok := fileBlocks[path]; !ok {
				continue // 事件文件不在 Traces 表（如事后 unread）→ 跳过
			}
			if plan, ok := diffPlans[path]; ok && plan.Keep {
				// 方案2在上方未能实际插入，下面将发送完整当前块；同步完整块缓存基线。
				trace.AdvanceTraceCache(chatLn, path)
			}
		} else if taskBlock == "" {
			continue
		}
		anchor := findEventAnchor(l, dbIDToElement, ev)
		if anchor == nil {
			fallbackPaths = append(fallbackPaths, path) // 不可锚定 → 顶部 fallback
			continue
		}
		g := groups[ev.MsgID]
		if g == nil {
			g = &eventInsertGroup{anchor: anchor}
			groups[ev.MsgID] = g
		} else if elementAfter(g.anchor, anchor) {
			g.anchor = anchor // 同一事件多工具调用时取最靠后的锚点
		}
		g.paths = append(g.paths, path)
	}
	for _, g := range groups {
		sort.Strings(g.paths)
		var frags []trace.FileBlock
		var extra strings.Builder
		for _, p := range g.paths {
			if p == "@task" {
				extra.WriteString(taskBlock)
				extra.WriteString("\n\n")
			} else if fb, ok := fileBlocks[p]; ok {
				frags = append(frags, fb)
			}
		}
		content, err := trace.RenderTraceBlock(frags)
		if err != nil {
			logger.Error("render trace event block error: %v", err)
			content = ""
		}
		if content != "" && extra.Len() > 0 {
			content += "\n\n"
		}
		content += strings.TrimSpace(extra.String())
		if content == "" {
			continue
		}
		l.InsertAfter(reqStruct.Message{Role: "user", Content: content}, g.anchor)
	}
	// 不可锚定事件 → 顶部 fallback
	sort.Strings(fallbackPaths)
	var fb []string
	for _, p := range fallbackPaths {
		if p == "@task" {
			if taskBlock != "" {
				fb = append(fb, taskBlock)
			}
			continue
		}
		if blk, ok := fileBlocks[p]; ok {
			if s, err := trace.RenderTraceBlock([]trace.FileBlock{blk}); err == nil {
				fb = append(fb, s)
			}
		}
	}
	return strings.Join(fb, "\n\n")
}

// findEventAnchor 返回事件内容块的插入锚点（内容块插到该元素之后）。
// 原生模式 + 全部工具调用轮次完整回放：事件 assistant 消息之后、连续 role:tool 结果消息的
// 「最后一个」（不区分 ToolCallID）。一个 assistant 可并行携带多个 tool_calls，其多个
// tool_result 必须彼此紧邻——若按单个 ToolCallID 匹配锚点，内容块会插进 tool_result 序列
// 中间，使后续 tool_result 与其 tool_use 被内容块隔开，OpenAI→Anthropic 代理会 400
// （tool_result must have a corresponding tool_use block in the previous message）。
// 锚定结果序列末尾可保证内容块永不夹在多个 tool_result 之间。遇到首个非 tool 消息即停止扫描。
// 否则（提示词模式 / 原生无 tool_calls 的纯文本 assistant）：事件 assistant 消息本身。
// 消息被 skip 或超出分页范围（dbIDToElement 无记录）→ 返回 nil（不可锚定）。
func findEventAnchor(l *list.List, dbIDToElement map[uint64]*list.Element, ev *structs.TraceEvent) *list.Element {
	msgEl := dbIDToElement[ev.MsgID]
	if msgEl == nil {
		return nil
	}
	if ev.ToolCallID != "" {
		var last *list.Element
		for e := msgEl.Next(); e != nil; e = e.Next() {
			m := e.Value.(reqStruct.Message)
			if m.Role != reqStruct.RoleTool {
				break
			}
			last = e
		}
		if last != nil {
			return last
		}
	}
	return msgEl
}

// elementAfter 判断元素 b 是否在元素 a 之后（链表顺序）。
func elementAfter(a, b *list.Element) bool {
	for e := a; e != nil; e = e.Next() {
		if e == b {
			return true
		}
	}
	return false
}

// betweenTokens 估算旧块锚点（最早事件）与 diff 锚点（最新事件）之间（含 diff 锚点、不含 prev 锚点）消息的 token 总量。
// 用于方案2 软条件复核：这段内容在方案1 因前缀失效全价重算，在方案2 命中缓存。
func betweenTokens(prev, diff *list.Element) int {
	total := 0
	for e := prev.Next(); e != nil; e = e.Next() {
		if m, ok := e.Value.(reqStruct.Message); ok {
			total += u.EstimateTokens(m.Content)
		}
		if e == diff {
			break
		}
	}
	return total
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
