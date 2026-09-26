package build

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

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

// max_completion_tokens（最大输出 token 数）的默认值与允许区间。
// 下限来自网关对推理模型最小输出预算的要求，上限避免单次请求申请过大输出。
const (
	defaultCompletionTokens = 16384
	minCompletionTokens     = 4096
	maxCompletionTokens     = 32768
)

// completionTokenLimit 计算请求里的 max_completion_tokens：未配置（<=0）时用默认值，
// 并夹到 [minCompletionTokens, maxCompletionTokens]。
// 注意 TokenLimit 是**上下文上限**（输入+输出的总预算），不是输出上限，
// 两者互相独立，因此这里不读 TokenLimit。
func completionTokenLimit(configured int32) int {
	v := int(configured)
	if v <= 0 {
		v = defaultCompletionTokens
	}
	if v < minCompletionTokens {
		v = minCompletionTokens
	}
	if v > maxCompletionTokens {
		v = maxCompletionTokens
	}
	return v
}

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
	// chatLn 通常是刚从库里读出的行，DB 是 gorm:"-" 的运行时字段（不会被 First 填充）。
	// 末尾落位的 trace 内容块要按 chatLn 回写注入锚点与旧端存档，缺了 DB 句柄会静默跳过，
	// 表现为块每轮都重新前移到末尾（前缀缓存白丢一轮）。这里补齐，不依赖调用方。
	if chatLn != nil && chatLn.DB == nil {
		chatLn.DB = db
	}
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
	// temperature=0 是合法的显式采样温度，仅 -1 表示"未设置"（此前 0 被一并忽略）
	if modelConfig.ProviderSpecificConfig.EnableTemperature && modelConfig.ModelTemperature != -1 {
		response.Temperature = &modelConfig.ModelTemperature
	}
	if modelConfig.ProviderSpecificConfig.EnableTopP && modelConfig.ModelTopP != -1 && modelConfig.ModelTopP != 0 {
		response.TopP = &modelConfig.ModelTopP
	}
	// max_completion_tokens：最大输出 token 数，取模型配置的 MaxCompletionTokens
	// 并夹到 [4096, 32768]。TokenLimit 是上下文上限，与输出上限无关，不参与计算。
	completionTokens := completionTokenLimit(modelConfig.MaxCompletionTokens)
	response.MaxCompletionTokens = &completionTokens
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
			if err := db.Where("`chat_id` = ? AND (`agent_id` = \"\" OR `agent_id` IS NULL)", chatID).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj).Error; err != nil {
				return nil, err
			}
		} else {
			if err := db.Where("`chat_id` = ? AND `agent_id` = ?", chatID, agentCode).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj).Error; err != nil {
				return nil, err
			}
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
	// lastRenderedMsgID 本轮实际进入请求体的最后一条消息 id（末尾落位块的注入锚点）。
	// 注意 role:tool 结果消息不登记 dbIDToElement，因此这里取"已登记消息"的最大 id：
	// findEventAnchor 会从它走到其后最后一条 role:tool，正好是列表末尾。
	var lastRenderedMsgID uint64
	exitFlag := false
	for offsetPage := range maxPage {
		var obj []structs.Messages
		if agentCode == "" {
			if err := db.Where("`chat_id` = ? AND (`agent_id` = \"\" OR `agent_id` IS NULL)", chatID).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj).Error; err != nil {
				return nil, err
			}
		} else {
			if err := db.Where("`chat_id` = ? AND `agent_id` = ?", chatID, agentCode).Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj).Error; err != nil {
				return nil, err
			}
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
				// 压缩完成后的摘要回放：截断点消息可能是 assistant（lastMsgID 由消息顺序决定）。
				// thinking 模式下每条 assistant 消息都必须携带 reasoning_content 字段，
				// 缺失即 400 "The content[].thinking in the thinking mode must be passed back to the API"。
				// 摘要不是模型的思考产物，用空串占位；user 角色不得带该字段。
				if modelConfig.EnableThinking && msg.Role == reqStruct.RoleAssistant {
					emptyThinking := ""
					msg.ReasoningContent = &emptyThinking
				}
				exitFlag = true
			} else {
				if v.Type == structs.MessagesRoleAgent {
					// assistant 历史消息回放原生 tool_calls —— content 保留文本 Delta，
					// 工具调用解析 ToolCallingJSONString 为 msg.ToolCalls（tool_call_id + function.name/arguments），
					// 与当前轮请求的 tools 参数/响应解析同一种格式。
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
			if v.ID > lastRenderedMsgID {
				lastRenderedMsgID = v.ID
			}
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
	// trace/@task 内容块按落位计划插入历史（事件锚点 / 差分双锚点 / 消息列表末尾）；
	// 锚点不可渲染时回退末尾，不再回退到"历史之前的顶部聚合"（见 insertEventContentBlocks 注释）。
	if chatLn.TemporyDataOfSession != nil {
		em, _ := chatLn.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent)
		plans, _ := chatLn.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*trace.AnchorPlan)
		// 事件表为空但仍有落位计划时必须继续：虚拟对象（@tree）可能整轮都没有任何事件，
		// 只按事件表判空会把计划好的内容块整批丢掉。
		if len(em) > 0 || len(plans) > 0 {
			prevEm, _ := chatLn.TemporyDataOfSession[structs.TempKeyTracePrevEvents].(map[string]*structs.TraceEvent)
			diffPlans, _ := chatLn.TemporyDataOfSession[structs.TempKeyTraceDiffPlan].(map[string]trace.DiffPlan)
			rebuilt := insertEventContentBlocks(responseDeltaList, dbIDToElement, lastRenderedMsgID, em, prevEm, diffPlans, plans, chatLn)
			// 方案1（放弃 diff 缓存、注入完整内容）之后，该文件在注入点之前的 edit 调用
			// 已被完整内容覆盖，从请求体里去掉（连同其结果）。
			omitSupersededEditCalls(responseDeltaList, dbIDToElement, rebuilt)
		}
	}
	// 内部运行期通知（后台任务结束 / shell 停止等）：作为**消息列表末尾**的独立块注入。
	// 它们变化频繁且与对话无关，一旦放进 system 消息，就会把 tools 之后的整个前缀
	// （含全部历史）打掉；放在末尾则只重算末尾这一小块，前缀缓存不受影响。
	if chatLn.TemporyDataOfSession != nil {
		if notices, _ := chatLn.TemporyDataOfSession[structs.TempKeySystemNotices].(string); strings.TrimSpace(notices) != "" {
			responseDeltaList.PushBack(reqStruct.Message{
				Role: "user",
				Content: "<!-- Alkaid System Notice -->\n" +
					"<!-- Internal runtime notice from Alkaid0, not a user instruction. -->\n" +
					strings.TrimSpace(notices),
			})
		}
	}
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
	logCacheFingerprint(response)
	return response, nil
}

// logCacheFingerprint 由 ALKAID0_DEBUG_CACHE=1 打开：每个请求打一行"消息指纹"，
// 相邻两请求对比即可定位前缀缓存是从哪一条消息开始断裂的（前缀缓存排查的第一手工具）。
func logCacheFingerprint(response *reqStruct.ChatCompletionRequest) {
	if os.Getenv("ALKAID0_DEBUG_CACHE") != "1" {
		return
	}
	toolsRaw, _ := json.Marshal(response.Tools)
	toolsSum := sha256.Sum256(toolsRaw)
	var builder strings.Builder
	for i, m := range response.Messages {
		// 哈希"完整序列化消息"（含 tool_calls / reasoning_content，与发给 provider 的字节一致）：
		// 只哈希 content 会把带不同 tool_calls 的 assistant 消息误判为相同。
		raw, err := json.Marshal(m)
		if err != nil {
			raw = []byte(m.Role + m.Content)
		}
		sum := sha256.Sum256(raw)
		if i > 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, "%d:%s:%d:%s", i, m.Role, len(raw), hex.EncodeToString(sum[:6]))
	}
	logger.Info("[cache] n=%d tools=%s msgs=%s", len(response.Messages), hex.EncodeToString(toolsSum[:6]), builder.String())
}

// storedToolCall 存储层工具调用项（tool_calling_json_string 内部格式 [{"name","id","parameters"}]）。
// 存储层为标准 encoding/json 序列化（nativeAcc.Origin()），此处直接解析，避免依赖 request 包（循环依赖）。
// id/name 用于回放调用结构；Parameters 保留真实参数并原样回放（见 replayToolArguments）。
type storedToolCall struct {
	Name       string          `json:"name"`
	ID         string          `json:"id"`
	Parameters json.RawMessage `json:"parameters"`
}

// replayToolArguments 把存储层参数回放为原生 tool_calls 的 arguments 字符串。
//
// 始终回放**完整参数**，不做任何形式的内容省略：省略占位符会被模型当成合法取值照抄，
// 两种形态都已在真实会话里复现：
//   - 整体换成 "..."：模型产出 {"arguments":"..."}，run 随即报
//     "[System] Parameter Error: type is required"；
//   - 换成 "<omitted: N chars>"：模型把该字面量当成 text 参数写进文件
//     （edit 把 4554 字符的内容写成了一行 "<omitted: 4554 chars>"，文件被污染）。
//
// 省略省下的那点上下文，不值得让模型把占位符学成真实取值；上下文膨胀交给摘要/压缩机制处理。
// 参数缺失（防御性分支，正常写入侧不会发生）回放为合法的空对象 {}。
func replayToolArguments(params json.RawMessage) string {
	if len(params) == 0 {
		return "{}"
	}
	return string(params)
}

// parseStoredToolCalls 解析存储层工具调用 JSON 为原生 tool_calls 消息。
// 参数一律完整回放（见 replayToolArguments），不省略任何字段。
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
		calls = append(calls, reqStruct.StreamToolCall{
			ID:   it.ID,
			Type: "function",
			Function: &reqStruct.StreamToolCallFunc{
				Name:      it.Name,
				Arguments: replayToolArguments(it.Parameters),
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
			if path == "" || path == "@task" {
				continue
			}
			if toolCallUnread(call) {
				delete(paths, path)
				continue
			}
			paths[path] = struct{}{}
		}
	}
	return paths, nil
}

// eventWindowQuery 构造回放窗口查询（按 id 倒序分页，遇到 summary 即截断）。
func eventWindowQuery(db *gorm.DB, chatID uint32, agentCode string) *gorm.DB {
	if agentCode == "" {
		return db.Where(`chat_id = ? AND (agent_id = "" OR agent_id IS NULL)`, chatID)
	}
	return db.Where("chat_id = ? AND agent_id = ?", chatID, agentCode)
}

// scanEventWindow 按 id 倒序分页读取事件窗口（最多 maxPage*readPageSize 条），
// 遇到 summary（压缩边界）即截断，返回按 id 正序排列的消息切片。
// 与 RequestBody 的回放窗口同源，保证事件位置与回放内容一一对应。
func scanEventWindow(db *gorm.DB, chatID uint32, agentCode string) ([]structs.Messages, error) {
	desc := make([]structs.Messages, 0, readPageSize*maxPage)
scan:
	for offsetPage := range maxPage {
		var obj []structs.Messages
		if err := eventWindowQuery(db, chatID, agentCode).
			Order("id DESC").Offset(offsetPage * readPageSize).Limit(readPageSize).Find(&obj).Error; err != nil {
			return nil, err
		}
		if len(obj) == 0 {
			break
		}
		for _, v := range obj {
			if v.Summary != "" {
				break scan
			}
			desc = append(desc, v)
		}
	}
	for i, j := 0, len(desc)-1; i < j; i, j = i+1, j-1 {
		desc[i], desc[j] = desc[j], desc[i]
	}
	return desc, nil
}

// tempPathFromToolResult 从工具结果的 return JSON 中提取 @temp 对象路径。
// run（前台/后台）、fetch、python task 等把产物写成 @temp 对象，并在结果里带 path 或 run_id；
// 非沙盒降级路径的 path 字段是命令输出本身，前缀不符会被这里过滤掉。
func tempPathFromToolResult(returnJSON string) string {
	if strings.TrimSpace(returnJSON) == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(returnJSON), &m); err != nil {
		return ""
	}
	for _, key := range []string{"path", "run_id"} {
		if s, ok := m[key].(string); ok && strings.HasPrefix(s, "@temp/") {
			return s
		}
	}
	return ""
}

// DetectTraceEvents 扫描回放窗口，为每个被 read/edit 交互过、或产生了 @temp 产物的路径
// 记录最近一次事件（TempKeyTraceEvents）与最早一次事件（TempKeyTracePrevEvents）。
//
// 事件来源两类（docs/trace-cache-spec.md §4.1）：
//  1. read/edit 工具调用的 path 参数（原有语义）；
//  2. run/fetch/python 等工具**结果**里的 @temp 路径——这类产物的路径不在调用参数里，只在结果中
//     出现，因此先做一次 callID → @temp 路径的正向合并，再按倒序扫描复用同一套"最新 + 最早"语义。
//
// 事件 MsgID 一律指向产生该结果的 assistant 消息：findEventAnchor 会走到该消息之后最后一条
// 连续 role:tool，因此并行工具调用的内容块不会被插进 tool 结果序列中间。
// 扫描遇到 summary（压缩边界）即停止——边界之前的历史不再回放，其 trace 也不该注入。
// @task 是虚拟任务对象，不属于 Traces 表。
func DetectTraceEvents(db *gorm.DB, session *structs.Chats, agentCode string) error {
	window, err := scanEventWindow(db, session.ID, agentCode)
	if err != nil {
		return err
	}

	// 正向合并：assistant 消息的工具调用 → 其 role:tool 结果里的 @temp 路径
	tempPathByCall := make(map[string]string)
	callInWindow := make(map[string]struct{})
	for _, v := range window {
		if v.Type == structs.MessagesRoleAgent && v.ToolCallingJSONString != "" {
			var calls []storedToolCall
			if err := json.Unmarshal([]byte(v.ToolCallingJSONString), &calls); err != nil {
				continue
			}
			for _, c := range calls {
				if c.ID != "" {
					callInWindow[c.ID] = struct{}{}
				}
			}
			continue
		}
		if v.Type != structs.MessagesRoleTool || v.Delta == "" {
			continue
		}
		results, err := parseStoredToolResults(v.Delta)
		if err != nil {
			continue
		}
		for _, r := range results {
			if r.ID == "" {
				continue
			}
			// 调用不在窗口内（消息已滑出回放范围）时不合成事件：锚点不可解析，交由拼接期裁剪
			if _, ok := callInWindow[r.ID]; !ok {
				continue
			}
			if p := tempPathFromToolResult(r.Return); p != "" {
				tempPathByCall[r.ID] = p
			}
		}
	}

	eventMap := make(map[string]*structs.TraceEvent)
	prevMap := make(map[string]*structs.TraceEvent)
	suppressed := make(map[string]bool)
	// 从新到旧扫描：先扫到的即最近事件；prevMap 反复覆盖，最后留下最早一条（方案2 旧块的固定锚点）
	for i := len(window) - 1; i >= 0; i-- {
		v := window[i]
		if v.Type != structs.MessagesRoleAgent || v.ToolCallingJSONString == "" {
			continue
		}
		var items []storedToolCall
		if err := json.Unmarshal([]byte(v.ToolCallingJSONString), &items); err != nil || len(items) == 0 {
			continue
		}
		for _, c := range items {
			path := ""
			isEdit := false
			isUnread := false
			switch {
			case c.Name == "read" || c.Name == "edit":
				path = toolCallPath(c)
				isEdit = c.Name == "edit"
				isUnread = !isEdit && toolCallUnread(c)
			case tempPathByCall[c.ID] != "":
				path = tempPathByCall[c.ID]
			}
			if path == "" {
				continue
			}
			if isUnread {
				if _, exists := eventMap[path]; exists {
					continue
				}
				suppressed[path] = true
				delete(prevMap, path)
				continue
			}
			if suppressed[path] {
				continue
			}
			ev := &structs.TraceEvent{
				MsgID:      v.ID,
				ToolCallID: c.ID,
				IsEdit:     isEdit,
				IsTask:     path == "@task",
			}
			if _, exists := eventMap[path]; exists {
				// 已记录最新事件，此处为更旧的事件：保留最早一条，作为方案2 旧块的固定锚点。
				// 旧块锚定「最早事件」位置，连续编辑时锚点不随次新事件漂移，前缀缓存才不被破坏。
				prevMap[path] = ev
				continue
			}
			eventMap[path] = ev
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
// 不复用 parseStoredToolCalls：那条路径返回的是回放用的 arguments 字符串，
// 这里需要直接解析参数对象取出 path。
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

func toolCallUnread(c storedToolCall) bool {
	if len(c.Parameters) == 0 {
		return false
	}
	var args map[string]any
	if err := json.Unmarshal(c.Parameters, &args); err != nil {
		return false
	}
	unread, _ := args["unread"].(bool)
	return unread
}

// eventInsertGroup 同一事件（一条 assistant 消息）内多个文件的内容块合并为一条 user 消息。
type eventInsertGroup struct {
	anchor *list.Element
	paths  []string
}

// insertEventContentBlocks 按事件把 trace/@task 内容块插入历史（紧跟最近 read/edit 事件之后），
// 返回不可锚定事件的顶部 fallback 内容（独立 user 消息，插在 addUserPrompt 之后）。
// 内容块在 PreHook 阶段已渲染并暂存于 chatLn.TemporyDataOfSession，此处只做拼装插入，不重复读盘。
//
// 第二个返回值是「方案1 实际注入完整内容」的 read 事件（path → 事件）：调用方据此省略该文件
// 在注入点之前的 edit 调用（见 omitSupersededEditCalls）。
// insertEventContentBlocks 按落位计划把 trace/@task 内容块插入历史，返回"实际注入了完整内容"的
// read 事件集合（供调用方省略被完整内容覆盖的历史 edit 调用）。
//
// 落位来源（docs/trace-cache-spec.md §4.2 / §4.4）：
//   - trace.AnchorPlan（trace 层决策）：Full=完整块锚指定消息；Diff=旧块+diff 双锚点；Tail=消息列表末尾；
//   - 无计划的 path（@task、未走 trace 层的调用路径）：差分计划优先，否则沿用"紧跟最新事件"的旧语义。
//
// 锚点不可渲染时一律回退到消息列表末尾，不再回退到"历史之前的顶部聚合"：顶部 fallback 会把内容块
// 放到整个对话之前，它一变，后续全部历史都失去前缀缓存（本次修复的核心）。
// tailMsgID 是本轮实际渲染的最后一条历史消息 id，用于回写末尾落位的注入锚点。
func insertEventContentBlocks(l *list.List, dbIDToElement map[uint64]*list.Element, tailMsgID uint64,
	eventMap, prevMap map[string]*structs.TraceEvent, diffPlans map[string]trace.DiffPlan,
	plans map[string]*trace.AnchorPlan, chatLn *structs.Chats) map[string]*structs.TraceEvent {

	fileBlocks, _ := chatLn.TemporyDataOfSession[structs.TempKeyTraceFileBlocks].(map[string]trace.FileBlock)
	taskBlock, _ := chatLn.TemporyDataOfSession[structs.TempKeyTaskEventBlock].(string)

	// findAnchor 把"消息 id"解析为插入锚点：该消息之后最后一条连续 role:tool，否则该消息本身。
	findAnchor := func(msgID uint64) *list.Element {
		if msgID == 0 {
			return nil
		}
		return findEventAnchor(l, dbIDToElement, &structs.TraceEvent{MsgID: msgID, ToolCallID: "anchor"})
	}
	renderBlock := func(fb trace.FileBlock) string {
		s, err := trace.RenderTraceBlock([]trace.FileBlock{fb})
		if err != nil {
			logger.Error("render trace event block error: %v", err)
			return ""
		}
		return s
	}

	renderedFull := make(map[string]struct{})    // 实际注入完整内容的 path（供 omit 判定）
	groups := make(map[uint64]*eventInsertGroup) // 同一锚点消息的多文件块合并为一条 user 消息
	tailPaths := make([]string, 0)

	// 注入集合 = 落位计划 ∪ 事件表：
	// 计划覆盖 trace 层本轮要注入的所有 path，其中虚拟对象（@tree）可能始终没有对应事件，
	// 若只遍历事件表就会被整轮丢弃（计划生成了却不执行）。事件表则保留旧的调用路径兼容。
	injectPaths := make([]string, 0, len(plans)+len(eventMap))
	seenPath := make(map[string]struct{}, len(plans)+len(eventMap))
	for path := range plans {
		if _, ok := seenPath[path]; ok {
			continue
		}
		seenPath[path] = struct{}{}
		injectPaths = append(injectPaths, path)
	}
	for path := range eventMap {
		if _, ok := seenPath[path]; ok {
			continue
		}
		seenPath[path] = struct{}{}
		injectPaths = append(injectPaths, path)
	}
	sort.Strings(injectPaths)

	for _, path := range injectPaths {
		ev := eventMap[path]
		plan := plans[path]
		if plan == nil {
			if ev == nil {
				continue // 既无计划也无事件：无可注入内容
			}
			// 无落位计划：差分计划优先（旧语义），否则紧跟最新事件
			if dp, ok := diffPlans[path]; ok && dp.Keep {
				if prev, ok := prevMap[path]; ok && prev != nil {
					plan = &trace.AnchorPlan{Mode: trace.AnchorDiff, MsgID: ev.MsgID, PrevMsgID: prev.MsgID}
				} else {
					// 差分候选缺少旧块锚点：退化为完整块，并推进完整块基线（旧语义）
					trace.AdvanceTraceCache(chatLn, path)
				}
			}
			if plan == nil {
				plan = &trace.AnchorPlan{Mode: trace.AnchorFull, MsgID: ev.MsgID, Full: true}
			}
		}
		if plan.Mode == trace.AnchorDiff || plan.Mode == trace.AnchorDiffTail {
			if dp, ok := diffPlans[path]; ok && dp.Keep {
				prevAnchor := findAnchor(plan.PrevMsgID)
				// 有事件承载 → diff 锚最新事件；无事件承载（@tree / 后台刷新）→ diff 放列表末尾
				var diffAnchor *list.Element
				if plan.Mode == trace.AnchorDiff {
					diffAnchor = findAnchor(plan.MsgID)
				} else {
					diffAnchor = l.Back()
				}
				if prevAnchor != nil && diffAnchor != nil && trace.KeepDiffPlan(dp, betweenTokens(prevAnchor, diffAnchor)) {
					if content := renderBlock(dp.OldBlock); content != "" {
						l.InsertAfter(reqStruct.Message{Role: "user", Content: content}, prevAnchor)
					}
					if content := renderBlock(dp.DiffBlock); content != "" {
						if plan.Mode == trace.AnchorDiff {
							l.InsertAfter(reqStruct.Message{Role: "user", Content: content}, diffAnchor)
						} else {
							l.PushBack(reqStruct.Message{Role: "user", Content: content})
						}
					}
					continue
				}
			}
			// 锚点不可用或软条件不满足 → 退化为完整块；模型将收到完整当前内容，缓存基线同步前移
			trace.AdvanceTraceCache(chatLn, path)
			if plan.Mode == trace.AnchorDiff {
				plan = &trace.AnchorPlan{Mode: trace.AnchorFull, MsgID: plan.MsgID, Full: true}
			} else {
				// 差分尾巴落空：完整块落到末尾（MsgID=0 → 由末尾落位分支处理并回写锚点）
				plan = &trace.AnchorPlan{Mode: trace.AnchorFull, Full: true}
			}
		}
		if plan.Mode == trace.AnchorTail {
			tailPaths = append(tailPaths, path)
			continue
		}
		anchor := findAnchor(plan.MsgID)
		if anchor == nil {
			// 锚点不可渲染 → 回退末尾（见函数注释）
			tailPaths = append(tailPaths, path)
			continue
		}
		g := groups[plan.MsgID]
		if g == nil {
			g = &eventInsertGroup{anchor: anchor}
			groups[plan.MsgID] = g
		} else if elementAfter(g.anchor, anchor) {
			g.anchor = anchor // 同一事件多工具调用时取最靠后的锚点
		}
		g.paths = append(g.paths, path)
	}
	// 同一 assistant 消息的内容块合并为一条 user 消息，合并内 path 排序保证字节稳定
	for _, g := range groups {
		sort.Strings(g.paths)
		var frags []trace.FileBlock
		var contributed []string
		var extra strings.Builder
		for _, p := range g.paths {
			if p == "@task" {
				if taskBlock != "" {
					extra.WriteString(taskBlock)
					extra.WriteString("\n\n")
				}
				continue
			}
			if fb, ok := fileBlocks[p]; ok {
				frags = append(frags, fb)
				contributed = append(contributed, p)
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
		for _, p := range contributed {
			renderedFull[p] = struct{}{}
		}
	}
	// 末尾落位：内容变了但没有新消息承载（后台刷新 / 外部改写）的块统一追加到列表最后，
	// 让每轮只重算"尾巴"，而不是从旧锚点起重算整段历史。按 path 排序保证字节稳定。
	sort.Strings(tailPaths)
	for _, p := range tailPaths {
		var content string
		if p == "@task" {
			content = strings.TrimSpace(taskBlock)
		} else if fb, ok := fileBlocks[p]; ok {
			content = renderBlock(fb)
		}
		if content == "" {
			continue
		}
		l.PushBack(reqStruct.Message{Role: "user", Content: content})
		renderedFull[p] = struct{}{}
		if chatLn == nil || tailMsgID == 0 {
			continue
		}
		// 锚点前移 + 旧端存档推进：下一轮内容未变时块留在原处（命中缓存）
		trace.SetTraceAnchor(chatLn, p, tailMsgID)
		trace.AdvanceTraceCache(chatLn, p)
	}
	rebuilt := make(map[string]*structs.TraceEvent)
	for path := range renderedFull {
		ev, ok := eventMap[path]
		if !ok || ev == nil || ev.IsTask || ev.IsEdit {
			continue
		}
		rebuilt[path] = ev
	}
	return rebuilt
}

// omitSupersededEditCalls 在「方案1：放弃 diff 缓存、注入完整内容」发生后，把该文件在注入事件
// 之前的所有 edit 调用连同其 role:"tool" 结果从请求体里去掉：注入的完整块已经代表文件当前内容，
// 逐次 edit 的参数（尤其是整文件 text）纯属重复占用上下文。
//
// rebuilt 由 insertEventContentBlocks 返回（path → 注入完整内容的 read 事件）。
// 只处理确实进入本次回放窗口的消息（dbIDToElement 里有的），且只省略注入点之前（dbID 更小）的调用；
// 注入点及其之后的 edit 保持回放。整个 assistant 轮次只剩被省略的调用时，连该消息一起移除。
func omitSupersededEditCalls(l *list.List, dbIDToElement map[uint64]*list.Element, rebuilt map[string]*structs.TraceEvent) {
	if len(rebuilt) == 0 {
		return
	}
	omitted := make(map[string]struct{})
	var emptied []*list.Element
	for dbID, el := range dbIDToElement {
		if el == nil {
			continue
		}
		m, ok := el.Value.(reqStruct.Message)
		if !ok || len(m.ToolCalls) == 0 {
			continue
		}
		kept := make([]reqStruct.StreamToolCall, 0, len(m.ToolCalls))
		removed := false
		for _, c := range m.ToolCalls {
			if c.ID != "" && c.Function != nil && c.Function.Name == "edit" {
				if ev, ok := rebuilt[toolCallArgumentPath(c.Function.Arguments)]; ok && dbID < ev.MsgID {
					omitted[c.ID] = struct{}{}
					removed = true
					continue
				}
			}
			kept = append(kept, c)
		}
		if !removed {
			continue
		}
		// 工具调用被省略光时必须置 nil：Message.ToolCalls 的 json tag 没有 omitempty，
		// 非 nil 空切片会序列化成 "tool_calls":[]，DeepSeek 直接 400
		// （Invalid 'messages[N].tool_calls': empty array. Expected an array with minimum length 1）。
		// "edit 后再 read 确认"这种自然操作即可触发：唯一调用被省略 + 消息带 thinking 时不会被整条移除。
		if len(kept) == 0 {
			m.ToolCalls = nil
		} else {
			m.ToolCalls = kept
		}
		// 与回放时的空 assistant 判定保持一致：没有任何可展示内容时整条消息移除。
		if len(m.ToolCalls) == 0 && m.Content == "" && (m.ReasoningContent == nil || *m.ReasoningContent == "") {
			emptied = append(emptied, el)
			continue
		}
		el.Value = m
	}
	for _, el := range emptied {
		l.Remove(el)
	}
	if len(omitted) == 0 {
		return
	}
	// 结果消息（含被终止调用的占位）按 tool_call_id 删除，保证 assistant 的每个 tool_call 仍有配对的 role:tool。
	for e := l.Front(); e != nil; {
		next := e.Next()
		if m, ok := e.Value.(reqStruct.Message); ok && m.Role == reqStruct.RoleTool {
			if _, hit := omitted[m.ToolCallID]; hit {
				l.Remove(e)
			}
		}
		e = next
	}
}

// toolCallArgumentPath 从回放后的工具调用 arguments JSON 里取 path 参数；取不到返回空串。
// 只用于判断 edit 作用于哪个文件（参数已是完整回放，不存在省略占位符）。
func toolCallArgumentPath(arguments string) string {
	if arguments == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(arguments), &m); err != nil {
		return ""
	}
	p, _ := m["path"].(string)
	return p
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
