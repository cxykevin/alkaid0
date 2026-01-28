package agent

import (
	_ "embed" // embed
	"errors"
	"fmt"

	"github.com/cxykevin/alkaid0/library/json"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/request/agents"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

const toolName = "agent"

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

type toolCallFlagTempory struct {
	NameOutputed      bool
	PromptOutputedLen int32
}

func buildPrompt(session *structs.Chats) (string, error) {
	return promptIn, nil
}
func buildPromptOut(session *structs.Chats) (string, error) {
	return promptOut, nil
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, error) {
	// 只在参数存在时输出，支持流式更新
	tmp, ok := session.TemporyDataOfRequest["tools:agent"]
	if !ok || tmp == nil {
		session.TemporyDataOfRequest["tools:agent"] = toolCallFlagTempory{}
		tmp = session.TemporyDataOfRequest["tools:agent"]
	}
	tmpObj := tmp.(toolCallFlagTempory)
	if namePtr, ok := mp["name"]; ok && namePtr != nil {
		if name, ok := (*namePtr).(string); ok {
			if !tmpObj.NameOutputed {
				fmt.Printf("Activate agent: %s\n", name)
				tmpObj.NameOutputed = true
			}
		}
	}

	if textPtr, ok := mp["prompt"]; ok && textPtr != nil {
		var textOut string
		if text, ok := (*textPtr).(string); ok {
			textOut = text
		}
		if text, ok := (*textPtr).(json.StringSlot); ok {
			textOut = string(text)
		}
		if textOut != "" && int(tmpObj.PromptOutputedLen) == 0 {
			activateStr := "Activate"
			if session.CurrentAgentID != "" {
				activateStr = "Dectivate"
			}
			fmt.Printf("%s prompt: ", activateStr)
		}
		if textOut != "" && int(tmpObj.PromptOutputedLen) < len(textOut) {
			fmt.Print(textOut[tmpObj.PromptOutputedLen:])
			tmpObj.PromptOutputedLen = int32(len(textOut))
		}
	}
	session.TemporyDataOfRequest["tools:agent"] = tmpObj
	return true, cross, nil
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

func load() string {
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            "activate_agent",
		UserDescription: promptIn,
		Parameters:      parasIn,
		ID:              "activate_agent",
		Enable: func(session *structs.Chats) bool {
			return session.CurrentAgentID == ""
		},
	})
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            "deactivate_agent",
		UserDescription: promptOut,
		Parameters:      parasOut,
		ID:              "deactivate_agent",
		Enable: func(session *structs.Chats) bool {
			return session.CurrentAgentID != ""
		},
	})
	actions.HookTool("activate_agent", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     buildPrompt,
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
			Func:     buildPromptOut,
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
