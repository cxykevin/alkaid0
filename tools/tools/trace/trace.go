package trace

import (
	_ "embed" // embed
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

const toolName = "trace"

//go:embed prompt.md
var prompt string

//go:embed trace.md
var tracePrompt string

var traceTempate *template.Template

var logger = log.New("tools:trace")

func init() {
	traceTempate = prompts.Load("tools:trace:trace", tracePrompt)
}

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
	FlagOutputed bool
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
	if untPtr, ok := mp["untrace"]; ok && untPtr != nil {
		if unt, ok := (*untPtr).(bool); ok {
			if !tmpObj.FlagOutputed {
				fmt.Printf("Untrace: %v\n", unt)
				tmpObj.FlagOutputed = true
			}
		}
	}
	session.TemporyDataOfRequest["tools:trace"] = tmpObj
	return true, cross, nil
}

// Trace 跟踪文件
func Trace(session *structs.Chats, mp map[string]*any, push []*any) (bool, []*any, map[string]*any, error) {
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
	path = filepath.Join(session.CurrentActivatePath, path)

	// 检查并获取untrace参数
	untracePtr, ok := mp["untrace"]
	var untrace bool
	if ok && untracePtr != nil {
		untrace, ok = (*untracePtr).(bool)
		if !ok || path == "" {
			untrace = false
		}
	}

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

	traceStr := "trace"
	if untrace {
		traceStr = "untrace"
	}
	logger.Info("%s file \"%s\" in ID=%d,agentID=%s", traceStr, path, session.ID, session.CurrentAgentID)

	if untrace {
		// 删数据库
		tx := session.DB.Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, path, session.CurrentAgentID).Delete(&structs.Traces{})
		err := tx.Error
		if err != nil {
			logger.Warn("delete trace failed: %v", err)
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, err
		}
		if tx.RowsAffected == 0 {
			// 没有找到
			boolx := false
			success := any(boolx)
			errMsg := any("no such trace")
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
	} else {
		// 检查文件是否存在
		stat, err := os.Stat(path)
		if err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any("file not exist")
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		// 文件过大(100K)
		if stat.Size() > 50*1024 {
			boolx := false
			success := any(boolx)
			errMsg := any("file too large")
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		// 读取文件内容
		content, err := os.ReadFile(path)
		if err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any("file read error: " + err.Error())
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		str := fileContentToString(content)
		if len(str) == 0 {
			boolx := false
			success := any(boolx)
			errMsg := any("file is empty or cannot readable (may be binary file)")
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}

		// 读取行数
		lines := strings.Split(str, "\n")
		if len(lines) > 2000 {
			boolx := false
			success := any(boolx)
			errMsg := any("file is too long")
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}

		// 更新 TraceID
		session.TraceID++
		// 写数据库
		trace := structs.Traces{
			ChatID:  session.ID,
			Path:    path,
			TraceID: session.TraceID,
			AgentID: session.CurrentAgentID,
		}
		session.DB.Save(&trace)
		err = session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("trace_id", session.TraceID).Error
		if err != nil {
			logger.Warn("update trace failed: %v", err)
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
	}

	// TODO: RAG trace

	// 读 db
	traces := []structs.Traces{}
	err := session.DB.Where("chat_id = ?", session.ID).Find(&traces).Error
	if err != nil {
		logger.Warn("read trace failed: %v", err)
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, err
	}
	session.TemporyDataOfSession["tools:trace"].(traceCache)[session.CurrentAgentID] = traces

	boolx := true
	success := any(boolx)
	return false, push, map[string]*any{
		"success": &success,
	}, nil
}

type templateStruct struct {
	Name   string
	Size   string
	Length uint32
	Text   string
}

type traceCache map[string]([]structs.Traces)

func buildTrace(session *structs.Chats) (string, error) {
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"]; !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"].(traceCache); !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"].(traceCache)[session.CurrentAgentID]; !ok {
		// 读 db
		traces := []structs.Traces{}
		err := session.DB.Where("chat_id = ? AND agent_id = ?", session.ID, session.CurrentAgentID).Find(&traces).Error
		if err != nil {
			return "", err
		}
		session.TemporyDataOfSession["tools:trace"].(traceCache)[session.CurrentAgentID] = traces
	}
	traces, ok := session.TemporyDataOfSession["tools:trace"].(traceCache)[session.CurrentAgentID]
	if !ok {
		return "", errors.New("failed to read traces from database")
	}

	var obj []templateStruct
	for _, traceObj := range traces {
		// 读取文件Stat
		stat, err := os.Stat(traceObj.Path)
		// 文件过大(100K)
		if err != nil {
			logger.Warn("trace warning: \"%s\" get stat error: %v", traceObj.Path, err)
			continue
		}
		// 文件过大(100K)
		if stat.Size() > 50*1024 {
			logger.Warn("trace warning: \"%s\" too large (%d)", traceObj.Path, stat.Size())
			continue
		}
		// 读取文件内容
		content, err := os.ReadFile(traceObj.Path)
		if err != nil {
			logger.Warn("trace warning: \"%s\" read error: %v", traceObj.Path, err)
			continue
		}
		str := fileContentToString(content)
		if len(str) == 0 {
			// logger.Warn("trace warning: \"%s\" empty", traceObj.Path)
			continue
		}

		// 读取行数
		lines := strings.Split(str, "\n")
		if len(lines) > 2000 {
			logger.Warn("trace warning: \"%s\" too long (%d)", traceObj.Path, len(lines))
			continue
		}
		allLenStrLen := len(fmt.Sprintf("%d", len(lines)))
		builder := strings.Builder{}
		for lineno, line := range lines {
			fmt.Fprintf(&builder, "%*d|%s\n", allLenStrLen, lineno+1, line)
		}
		// 转换为字符串
		obj = append(obj, templateStruct{
			Name:   traceObj.Path,
			Size:   fmt.Sprintf("%d", stat.Size()),
			Length: uint32(len(content)),
			Text:   builder.String(),
		})
	}
	traceList := prompts.Render(traceTempate, obj)
	return traceList, nil
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
			Func:     Trace,
		},
	})
	actions.HookTool("", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     buildTrace,
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
	return toolName
}

func init() {
	index.AddIndex(load)
}
