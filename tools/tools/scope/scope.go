package scope

import (
	_ "embed" // embed
	"errors"
	"fmt"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

const toolName = "scope"

//go:embed prompt.md
var prompt string

var logger = log.New("tools:scope")

var paras = map[string]parser.ToolParameters{
	"name": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The **exact name** of the scope will be enabled or disabled.",
	},
	"disable": {
		Type:        parser.ToolTypeBoolen,
		Required:    false,
		Description: "Disable the scopes. Default is false.",
	},
}

type toolCallFlagTempory struct {
	NameOutputed bool
	FlagOutputed bool
}

func buildPrompt(session *structs.Chats) (string, error) {
	return prompt, nil
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, error) {
	// 只在参数存在时输出，支持流式更新
	tmp, ok := session.TemporyDataOfRequest["tools:scope"]
	if !ok || tmp == nil {
		session.TemporyDataOfRequest["tools:scope"] = toolCallFlagTempory{}
		tmp = session.TemporyDataOfRequest["tools:scope"]
	}
	tmpObj := tmp.(toolCallFlagTempory)
	if namePtr, ok := mp["name"]; ok && namePtr != nil {
		if name, ok := (*namePtr).(string); ok {
			if !tmpObj.NameOutputed {
				fmt.Printf("Trace scope: %s\n", name)
				tmpObj.NameOutputed = true
			}
		}
	}
	if untPtr, ok := mp["untrace"]; ok && untPtr != nil {
		if unt, ok := (*untPtr).(bool); ok {
			if !tmpObj.FlagOutputed {
				fmt.Printf("Untrace: %v\n", unt)
				tmpObj.FlagOutputed = true
			}
		}
	}
	session.TemporyDataOfRequest["tools:scope"] = tmpObj
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

func useScope(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
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

	// 检查并获取untrace参数
	untracePtr, ok := mp["untrace"]
	var disable bool
	if ok && untracePtr != nil {
		disable, ok = (*untracePtr).(bool)
		if !ok || name == "" {
			disable = false
		}
	}

	enableString := "enable"
	if disable {
		enableString = "disable"
	}

	if name == "" {
		boolx := false
		success := any(boolx)
		errMsg := any("couldn't enable or disable default scope")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	logger.Info("%s scope \"%s\" in ID=%d,agentID=%s", enableString, name, session.ID, session.CurrentAgentID)

	if name == "" {
		boolx := false
		success := any(boolx)
		errMsg := any("couldn't enable or disable default scope")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	if disable {
		err = actions.DisableScope(session, name)
	} else {
		err = actions.EnableScope(session, name)
	}

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
		Name:            toolName,
		UserDescription: prompt,
		Parameters:      paras,
		ID:              toolName,
	})
	actions.HookTool(toolName, &toolobj.Hook{
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
			Func:     useScope,
		},
	})
	return toolName
}

func init() {
	index.AddIndex(load)
}
