package agent

import (
	_ "embed" // embed
	"errors"
	"fmt"
	"text/template"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	u "github.com/cxykevin/alkaid0/utils"

	agents "github.com/cxykevin/alkaid0/provider/request/agents/actions"
	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

const toolName = "agent"

//go:embed agents_prompt.md
var agentPrompt string

var agentsTemplate *template.Template = prompts.Load("tools:agent:agents", agentPrompt)

//go:embed prompt.md
var promptMan string

//go:embed prompt_in.md
var promptIn string

//go:embed prompt_out.md
var promptOut string

var logger = log.New("tools:agent")

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
var parasOut = map[string]parser.ToolParameters{
	"prompt": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The prompt the main agent will use.",
	},
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
	session.ToolCallingContext[toolCallID] = respObj
	session.ToolCallingType[toolCallID] = "agent"

	return true, cross, nil
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	currToolName := u.Ternary(session.CurrentAgentID != "", "activate_agent", "deactivate_agent")
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
		"name":      currToolName,
		"messageID": session.CurrentMessageID,
		"args": u.H{
			"name":   nameVal,
			"prompt": promptVal,
		},
	}}
	session.ToolCallingContext[toolCallID] = respObj
	session.ToolCallingType[toolCallID] = currToolName
	return true, cross, nil
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

func unuseAgent(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	logger.Info("deactivate agent \"%s\" in ID=%d", session.CurrentAgentID, session.ID)

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

	err = agents.DeactivateAgent(session, prompt)
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

type agentTemplate struct {
	Agents []struct {
		Name string
		Path string
		Tag  string
	}
	Tags []struct {
		Name        string
		Description string
	}
}

func buildGlobalPrompt(session *structs.Chats) (string, error) {
	tmpl := agentTemplate{}
	listAgent, err := agents.ListAgent(session)
	if err != nil {
		return "", err
	}
	tmpl.Agents = make([]struct {
		Name string
		Path string
		Tag  string
	}, len(listAgent))
	for i, agent := range listAgent {
		tmpl.Agents[i].Name = agent.ID
		tmpl.Agents[i].Path = agent.BindPath
		tmpl.Agents[i].Tag = agent.AgentID
	}

	tmpl.Tags = make([]struct {
		Name        string
		Description string
	}, len(agentconfig.GetAgentConfigMap()))
	idx := 0
	for i, agent := range agentconfig.GetAgentConfigMap() {
		tmpl.Tags[idx].Name = i
		tmpl.Tags[idx].Description = agent.AgentDescription
		idx++
	}
	return prompts.Render(agentsTemplate, tmpl), nil
}

func enableActivate(session *structs.Chats) bool {
	return session.CurrentAgentID == ""
}

func enableDeactivate(session *structs.Chats) bool {
	return session.CurrentAgentID != ""
}

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
	actions.HookTool("", &toolobj.Hook{
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
	})
	actions.HookTool("agent", &toolobj.Hook{
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
	})
	actions.HookTool("activate_agent", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     useAgent,
		},
	})
	actions.HookTool("deactivate_agent", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     unuseAgent,
		},
	})
	return toolName
}

func init() {
	index.AddIndex(load)
}
