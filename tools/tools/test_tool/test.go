package edit

import (
	_ "embed" // embed
	"errors"
	"fmt"
	"os"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	u "github.com/cxykevin/alkaid0/utils"
)

const toolName = "test"

//go:embed prompt.md
var prompt string

var logger = log.New("tools:test")

var paras = map[string]parser.ToolParameters{}

func buildPrompt(session *structs.Chats) (string, error) {
	return prompt, nil
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	fmt.Printf("Test Tool Update: %v\n", mp)

	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	respObj := []u.H{{
		"type": "content",
		"content": u.H{
			"type": "text",
			"text": "",
		},
	}, {
		"type":      "alk.cxykevin.top/calling_info",
		"name":      toolName,
		"messageID": session.CurrentMessageID,
		"args":      u.H{},
	}}
	session.ToolCallingContext[toolCallID] = respObj
	session.ToolCallingType[toolCallID] = toolName

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
	logger.Info("test tool called in ID=%d,agentID=%s", session.ID, session.CurrentAgentID)

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

func load() string {
	if os.Getenv("ALKAID0_TEST_TOOL_ENABLETESTTOOL") == "true" {
		actions.AddScope("test", "Test Tool Scope")
		actions.AddTool(&toolobj.Tools{
			Scope:           "test",
			Name:            toolName,
			UserDescription: prompt,
			Parameters:      paras,
			ID:              toolName,
		})
		actions.HookTool(toolName, &toolobj.Hook{
			Scope: "",
			PreHook: toolobj.PreHookFunction{
				Priority: 100000,
				Func:     buildPrompt,
			},
			OnHook: toolobj.OnHookFunction{
				Priority: 100000,
				Func:     updateInfo,
			},
			PostHook: toolobj.PostHookFunction{
				Priority: 100000,
				Func:     useScope,
			},
		})
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
