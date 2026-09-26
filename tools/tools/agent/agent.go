package agent

import (
	_ "embed" // embed
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/tools/index"
	u "github.com/cxykevin/alkaid0/utils"

	agents "github.com/cxykevin/alkaid0/provider/request/agents/actions"
	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// toolName 工具注册名称
const toolName = "agent"

//go:embed agents_prompt.md
var agentPrompt string

// agentsTemplate 子代理管理功能的提示词模板
var agentsTemplate *template.Template = prompts.Load("tools:agent:agents", agentPrompt)

//go:embed prompt.md
var promptMan string

//go:embed prompt_in.md
var promptIn string

//go:embed prompt_out.md
var promptOut string

// logger 包级日志对象
var logger = log.New("tools:agent")

// parasIn 激活/停用子代理的入参定义
var parasIn = map[string]parser.ToolParameters{
	"name": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The **exact name** of the agent will be activate. Must be the first parameter.",
	},
	"prompt": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The prompt the subagent will use.",
	},
}

// parasOut 子代理输出参数的入参定义
var parasOut = map[string]parser.ToolParameters{
	"prompt": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The prompt the main agent will use.",
	},
	// parasMan 子代理管理（增删改）的入参定义
}
var parasMan = map[string]parser.ToolParameters{
	"name": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The **exact name** of the agent instance will be created or deleted. Must be the first parameter.",
	},
	"tag": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: "The tag agent used. It decided the model and the global prompt which agent instance will be used. **Required if the agent instance will be created.**",
	},
	"path": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: "The path will agent be binded. The subagent instance can only edit files in the path. **Required if the agent instance will be created.**",
	},
	"delete": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "Delete the subagent instance. Default is false.",
	},
}

// func buildPrompt(session *structs.Chats) (string, error) {
// 	return promptIn, nil
// }
// func buildPromptOut(session *structs.Chats) (string, error) {
// 	return promptOut, nil
// updateAgentInfo 处理子代理管理操作的调用信息记录
// }

func updateAgentInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {

	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	respString := ""
	var nameVal *string
	var tagVal *string
	var deleteVal *bool
	if namePtr, ok := mp["name"]; ok && namePtr != nil {
		if name, ok := (*namePtr).(string); ok {
			respString += "Name: " + name + "\n"
			nameVal = &name
		}
	}
	if tagPtr, ok := mp["tag"]; ok && tagPtr != nil {
		if tag, ok := (*tagPtr).(string); ok {
			respString += "Tag: " + tag + "\n"
			tagVal = &tag
		}
	}
	if detelePtr, ok := mp["delete"]; ok && detelePtr != nil {
		if deletev, ok := (*detelePtr).(bool); ok {
			respString += "Delete: " + u.Ternary(deletev, "true", "false") + "\n"
			deleteVal = &deletev
		}
	}
	respObj := []u.H{{
		"type": "content",
		"content": u.H{
			"type": "text",
			"text": respString,
		},
	}, {
		"type":      "alk.cxykevin.top/calling_info",
		"name":      "agent",
		"messageID": session.CurrentMessageID,
		"args": u.H{
			"name":   nameVal,
			"tag":    tagVal,
			"delete": deleteVal,
		},
	}}
	session.SetToolCalling(toolCallID, respObj, "agent")

	return true, cross, nil
}

// updateInfo 处理激活/停用子代理的调用信息记录。
// toolDisplayName 为模型实际调用的工具名（activate_agent / deactivate_agent）：
// 展示名必须以调用名为准，否则直播的标题/名称依赖 session 状态，与 session/resume
// 回放（只能用落库的工具名）不一致。
func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string, toolDisplayName string) (bool, []*any, error) {
	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	respString := ""
	var nameVal *string
	var promptVal *string
	if namePtr, ok := mp["name"]; ok && namePtr != nil {
		if name, ok := (*namePtr).(string); ok {
			respString += "Name: " + name + "\n"
			nameVal = &name
		}
	}
	if promptPtr, ok := mp["prompt"]; ok && promptPtr != nil {
		if prompt, ok := (*promptPtr).(string); ok {
			respString += "Prompt: " + prompt + "\n"
			promptVal = &prompt
		}
	}
	respObj := []u.H{{
		"type": "content",
		"content": u.H{
			"type": "text",
			"text": respString,
		},
	}, {
		"type":      "alk.cxykevin.top/calling_info",
		"name":      toolDisplayName,
		"messageID": session.CurrentMessageID,
		"args": u.H{
			"name":   nameVal,
			"prompt": promptVal,
		},
	}}
	session.SetToolCalling(toolCallID, respObj, toolDisplayName)
	return true, cross, nil
}

// updateActivateInfo 注册给 activate_agent 的 OnHook。
func updateActivateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	return updateInfo(session, mp, cross, toolID, "activate_agent")
}

// updateDeactivateInfo 注册给 deactivate_agent 的 OnHook。
func updateDeactivateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	return updateInfo(session, mp, cross, toolID, "deactivate_agent")
}

// agentNameMaxLen 子代理实例名长度上限（按字符计）。
const agentNameMaxLen = 64

// validateAgentNameChars 白名单校验实例名本身：只允许字母/数字/'-'/'_'/'.'
// （允许中文等 Unicode 字母，保持既有合法名字可用），拒绝空白、控制字符、路径
// 分隔符、引号与尖括号等结构性字符——实例名会作为 <instance name="..."> 渲染进
// 全局提示词，也会作为 SubAgents 主键，这些字符可以被用来做提示注入。
func validateAgentNameChars(name string) error {
	if name == "" {
		return errors.New("invalid or empty name parameter")
	}
	if utf8.RuneCountInString(name) > agentNameMaxLen {
		return fmt.Errorf("invalid name: length must not exceed %d characters", agentNameMaxLen)
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("invalid name %q: only letters, digits, '-', '_' and '.' are allowed (no whitespace, path separators or control characters)", name)
	}
	// 纯点名（"."、".."、"..."）同时是路径穿越语义，禁止
	if strings.Trim(name, ".") == "" {
		return fmt.Errorf("invalid name %q: name cannot consist of dots only", name)
	}
	return nil
}

// validateAgentName 创建/更新实例时的完整校验：字符白名单 + 与已有 agent tag 重名检测。
// 实例名与 tag 是两个命名空间，但重名会让提示词里的 <agents> 与 <agent_tags> 混淆，
// 模型可能把实例名当成 tag 使用，因此这里一并拒绝。
func validateAgentName(name string) error {
	if err := validateAgentNameChars(name); err != nil {
		return err
	}
	if _, ok := agentconfig.GetAgentConfig(name); ok {
		return fmt.Errorf("invalid name %q: conflicts with an existing agent tag, choose another name", name)
	}
	return nil
}

// checkAgentActiveInOtherChat 检查子代理实例是否正被其它会话激活使用。
// SubAgents 表当前没有 chat 归属列（storage/structs/subagents.go 的 ChatID 被注释掉），
// 无法直接按 chat 过滤实例；唯一可判定的跨会话占用关系是 Chats.NowAgent。改/删别的
// 会话正在使用的实例会让对方会话的绑定路径/配置/tag 静默变化，删除后 now_agent 悬空
// 还会导致对方会话无法加载，因此这里拒绝跨会话改动；本会话自己的实例不受影响。
func checkAgentActiveInOtherChat(session *structs.Chats, name string) error {
	if session == nil || session.DB == nil {
		return nil
	}
	var count int64
	err := session.DB.Model(&structs.Chats{}).
		Where("now_agent = ? AND id <> ?", name, session.ID).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("agent instance %q is currently active in another chat; deactivate it there first", name)
	}
	return nil
}

func editAgent(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	name, err := CheckName(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 检查是否删除
	deletePtr, ok := mp["delete"]
	if ok && deletePtr != nil {
		if delete, ok := (*deletePtr).(bool); ok && delete {
			// 跨会话保护：别的会话正在激活使用的实例不能删，否则对方会话的 now_agent 悬空
			if err := checkAgentActiveInOtherChat(session, name); err != nil {
				boolx := false
				success := any(boolx)
				errMsg := any(err.Error())
				return false, cross, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			logger.Info("delete agent instance \"%s\" in ID=%d", name, session.ID)
			err := agents.DeleteAgent(session, name)
			if err != nil {
				boolx := false
				success := any(boolx)
				errMsg := any(err.Error())
				return false, cross, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}

			boolx := true
			success := any(boolx)
			return false, cross, map[string]*any{
				"success": &success,
			}, nil
		}
	}

	// 创建/更新前做白名单校验：名字会渲染进全局提示词模板并作为实例主键，
	// 非法字符可被用于提示注入或构造越权配置键；删除分支不校验，保证历史遗留的
	// 非法名实例仍能被清理。
	if err := validateAgentName(name); err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 检查 tag 参数
	tagPtr, ok := mp["tag"]
	if !ok || tagPtr == nil {
		boolx := false
		success := any(boolx)
		errMsg := any("missing tag parameter for creating/updating agent")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	tag, ok := (*tagPtr).(string)
	if !ok || tag == "" {
		boolx := false
		success := any(boolx)
		errMsg := any("invalid or empty tag parameter")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 检查 path 参数
	pathPtr, ok := mp["path"]
	if !ok || pathPtr == nil {
		boolx := false
		success := any(boolx)
		errMsg := any("missing path parameter for creating/updating agent")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	path, ok := (*pathPtr).(string)
	if !ok || path == "" {
		boolx := false
		success := any(boolx)
		errMsg := any("invalid or empty path parameter")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	logger.Info("edit agent instance \"%s\" with tag \"%s\" and path \"%s\" in ID=%d", name, tag, path, session.ID)

	// 先检查是否已存在
	var existingAgent structs.SubAgents
	err = session.DB.Where("id = ?", name).First(&existingAgent).Error
	if err == nil {
		// 跨会话保护：不允许改动别的会话正在使用的实例（其绑定路径/配置/tag
		// 被换掉后，对方会话重新加载会静默使用新配置）。
		if guardErr := checkAgentActiveInOtherChat(session, name); guardErr != nil {
			boolx := false
			success := any(boolx)
			errMsg := any(guardErr.Error())
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		// 已存在，使用 UpdateAgent 更新
		err = agents.UpdateAgent(session, name, tag, path)
		if err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
	} else {
		// 不存在，使用 AddAgent 创建
		err = agents.AddAgent(session, name, tag, path)
		if err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
	}

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

// CheckName 处理名称
func CheckName(mp map[string]*any) (string, error) {
	// 检查并获取 name 参数
	namePtr, ok := mp["name"]
	if !ok || namePtr == nil {
		return "", errors.New("missing name parameter")
	}
	name, ok := (*namePtr).(string)
	if !ok || name == "" {
		return "", errors.New("invalid or empty name parameter")
	}
	return name, nil
}

// CheckPrompt 处理名称
func CheckPrompt(mp map[string]*any) (string, error) {
	// 检查并获取 name 参数
	pmtPtr, ok := mp["prompt"]
	if !ok || pmtPtr == nil {
		return "", errors.New("missing prompt parameter")
	}
	prompt, ok := (*pmtPtr).(string)
	if !ok || prompt == "" {
		return "", errors.New("invalid or empty napromptme parameter")
	}
	return prompt, nil
}

// useAgent 激活一个子代理实例，将其绑定到当前会话
func useAgent(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	name, err := CheckName(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	prompt, err := CheckPrompt(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	logger.Info("use agent \"%s\" in ID=%d", name, session.ID)

	err = agents.ActivateAgent(session, name, prompt)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

// unuseAgent 停用当前会话的子代理实例
func unuseAgent(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	prompt, err := CheckPrompt(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// A stale tool call may arrive after the previous deactivation already
	// cleared the session. Treat it as an idempotent success and do not write
	// another communication message or start another summary.
	if session.CurrentAgentID == "" && session.NowAgent == "" {
		boolx := true
		success := any(boolx)
		return false, cross, map[string]*any{
			"success": &success,
		}, nil
	}

	if err := agents.DeactivateAgent(session, prompt); err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

// agentPromptEntry 全局提示词中的子代理实例条目
type agentPromptEntry struct {
	Name string
	Path string
	Tag  string
}

// agentPromptTag 全局提示词中的子代理 tag 条目
type agentPromptTag struct {
	Name        string
	Description string
}

// agentTemplate 子代理全局提示词模板的数据结构
type agentTemplate struct {
	Agents []agentPromptEntry
	Tags   []agentPromptTag
}

// buildGlobalPrompt 构建包含所有子代理信息的全局提示词
func buildGlobalPrompt(session *structs.Chats) (string, error) {
	tmpl := agentTemplate{}
	listAgent, err := agents.ListAgent(session)
	if err != nil {
		return "", err
	}
	for _, agent := range listAgent {
		// 读路径同样做字符白名单过滤：写入校验加固之前落库的历史实例名字可能含
		// 引号/换行等结构字符，原样渲染会破坏 <instance> 属性结构并造成提示注入。
		// 这里只做字符校验（不做 tag 重名检测），避免隐藏历史遗留但结构安全的实例。
		if err := validateAgentNameChars(agent.ID); err != nil {
			logger.Warn("skip invalid agent instance in prompt: %v", err)
			continue
		}
		tmpl.Agents = append(tmpl.Agents, agentPromptEntry{
			Name: agent.ID,
			Path: agent.BindPath,
			Tag:  agent.AgentID,
		})
	}

	// Tag 列表按名字排序后再渲染：配置是 map，直接 range 会让全局注入块每轮
	// 以不同顺序输出同一批 tag —— 前部块字节不稳定，整段对话历史失去前缀缓存
	// （docs/trace-cache-spec.md §4.6 确定性要求）。
	tagNames := make([]string, 0, len(agentconfig.GetAgentConfigMap()))
	for name := range agentconfig.GetAgentConfigMap() {
		tagNames = append(tagNames, name)
	}
	sort.Strings(tagNames)
	for _, name := range tagNames {
		agent := agentconfig.GetAgentConfigMap()[name]
		tmpl.Tags = append(tmpl.Tags, agentPromptTag{
			Name:        name,
			Description: agent.AgentDescription,
		})
	}
	rendered, err := prompts.Render(agentsTemplate, tmpl)
	if err != nil {
		return "", err
	}
	return rendered, nil
}

// enableActivate 判断当前会话是否允许激活子代理（无活跃子代理时）
func enableActivate(session *structs.Chats) bool {
	return session.CurrentAgentID == ""
}

// enableDeactivate 判断当前会话是否允许停用子代理（有活跃子代理时）
func enableDeactivate(session *structs.Chats) bool {
	return session.CurrentAgentID != ""
}

// load 注册 agent 工具及其钩子函数到工具系统
func load() string {
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            "agent",
		UserDescription: promptMan,
		Parameters:      parasMan,
		ID:              "agent",
		Enable:          enableActivate,
	})
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            "activate_agent",
		UserDescription: promptIn,
		Parameters:      parasIn,
		ID:              "activate_agent",
		Enable:          enableActivate,
	})
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            "deactivate_agent",
		UserDescription: promptOut,
		Parameters:      parasOut,
		ID:              "deactivate_agent",
		Enable:          enableDeactivate,
	})
	if err := actions.HookTool("", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     buildGlobalPrompt,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     nil,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     nil,
		},
	}); err != nil {
		panic(err)
	}
	if err := actions.HookTool("agent", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateAgentInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     editAgent,
		},
	}); err != nil {
		panic(err)
	}
	if err := actions.HookTool("activate_agent", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateActivateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     useAgent,
		},
	}); err != nil {
		panic(err)
	}
	if err := actions.HookTool("deactivate_agent", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateDeactivateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     unuseAgent,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
