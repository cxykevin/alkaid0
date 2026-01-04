package edit

import (
	_ "embed" // embed
	"encoding/json"
	"fmt"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

const toolName = "edit"

func unwrap[T any](args T, err error) T {
	if err != nil {
		panic(err)
	}
	return args
}

//go:embed prompt.md
var prompt string

var paras = map[string]parser.ToolParameters{
	"path": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The path of the file or virtual object to be edited. A new file will be created if it does not exist. **must be a RELATIVE path**. Must Be First Parameter",
	},
	"target": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: `Must Be Second Parameter`,
	},
	"text": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: `Replacement or appended text. Must Be Last Parameter`,
	},
}

func buildPrompt() (string, error) {
	return prompt, nil
}
func updateInfo(mp map[string]*any, cross []*any) (bool, []*any, error) {
	fmt.Printf("Edit Info: %v\n", string(unwrap(json.Marshal(mp))))
	return true, cross, nil
}
func writeFile(mp map[string]*any, push []*any) (bool, []*any, map[string]*any, error) {
	fmt.Printf("Edit File: %v\n", string(unwrap(json.Marshal(mp))))
	boolx := true
	success := any(boolx)
	return false, push, map[string]*any{
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
			Func:     writeFile,
		},
	})
	return toolName
}

func init() {
	index.AddIndex(load)
}
