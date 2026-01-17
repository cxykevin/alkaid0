package trace

import (
	_ "embed" // embed
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

const toolName = "trace"

//go:embed prompt.md
var prompt string

var paras = map[string]parser.ToolParameters{
	"untrace": {
		Type:        parser.ToolTypeBoolen,
		Required:    false,
		Description: "Whether to untrace the file. Default is false.",
	},
	"path": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The path of the file will be traced or untraced. **must be a RELATIVE path**. '..' is not allowed.",
	},
}

func buildPrompt(session *structs.Chats) (string, error) {
	return prompt, nil
}

type toolCallFlagTempory struct {
	PathOutputed bool
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, error) {
	// 只在参数存在时输出，支持流式更新
	tmp, ok := session.TemporyDataOfRequest["tools:trace"]
	if !ok || tmp == nil {
		session.TemporyDataOfRequest["tools:trace"] = toolCallFlagTempory{}
		tmp = session.TemporyDataOfRequest["tools:trace"]
	}
	tmpObj := tmp.(toolCallFlagTempory)
	if pathPtr, ok := mp["path"]; ok && pathPtr != nil {
		if path, ok := (*pathPtr).(string); ok {
			if !tmpObj.PathOutputed {
				fmt.Printf("Trace path: %s\n", path)
				tmpObj.PathOutputed = true
			}
		}
	}
	return true, cross, nil
}

func traceFile(session *structs.Chats, mp map[string]*any, push []*any) (bool, []*any, map[string]*any, error) {
	// 检查并获取path参数
	pathPtr, ok := mp["path"]
	if !ok || pathPtr == nil {
		boolx := false
		success := any(boolx)
		errMsg := any("missing path parameter")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	path, ok := (*pathPtr).(string)
	if !ok || path == "" {
		boolx := false
		success := any(boolx)
		errMsg := any("invalid or empty path parameter")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// var untrace bool
	// if untracePtr, ok := mp["untrace"]; ok && untracePtr != nil {
	// 	untrace, ok = (*untracePtr).(bool)
	// 	if !ok {
	// 		untrace := false
	// 	}
	// }

	// 检查path
	if strings.Contains(path, "..") {
		boolx := false
		success := any(boolx)
		errMsg := any("path cannot contains '..'")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	if strings.HasPrefix(path, "/") ||
		strings.HasPrefix(path, "\\") ||
		strings.HasPrefix(path, "~") ||
		strings.Contains(path, ":") ||
		strings.Contains(path, "*") ||
		strings.Contains(path, "?") ||
		strings.Contains(path, "\"") ||
		strings.Contains(path, "<") ||
		strings.Contains(path, ">") ||
		strings.Contains(path, "|") ||
		strings.Contains(path, "\n") ||
		strings.Contains(path, "\r") ||
		strings.Contains(path, "\t") {
		boolx := false
		success := any(boolx)
		errMsg := any("path must be a correct and relative path")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// TODO v2: Trace file
	// TODO v2: Trace to database
	// TODO v3: RAG trace

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
			Func:     traceFile,
		},
	})
	return toolName
}

func init() {
	index.AddIndex(load)
}
