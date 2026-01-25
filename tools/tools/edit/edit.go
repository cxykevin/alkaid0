package edit

import (
	"bufio"
	"bytes"
	_ "embed" // embed
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/library/json"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
)

const toolName = "edit"

//go:embed prompt.md
var prompt string

var logger = log.New("tools:edit")

var paras = map[string]parser.ToolParameters{
	"path": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The path of the file or virtual object to be edited. A new file will be created if it does not exist. **must be a RELATIVE path**. '..' is not allowed. Must Be First Parameter",
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

// PassInfo 传递信息
type PassInfo struct {
	From        string
	Description string
	Parameters  map[string]any
}

func buildPrompt(session *structs.Chats) (string, error) {
	return prompt, nil
}

type toolCallFlagTempory struct {
	PathOutputed    bool
	TargetOutputed  bool
	TextOutputedLen int32
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, error) {
	tmp, ok := session.TemporyDataOfRequest["tools:edit"]
	if !ok || tmp == nil {
		session.TemporyDataOfRequest["tools:edit"] = toolCallFlagTempory{}
		tmp = session.TemporyDataOfRequest["tools:edit"]
	}
	tmpObj := tmp.(toolCallFlagTempory)
	if pathPtr, ok := mp["path"]; ok && pathPtr != nil {
		if path, ok := (*pathPtr).(string); ok {
			if !tmpObj.PathOutputed {
				fmt.Printf("Edit path: %s\n", path)
				tmpObj.PathOutputed = true
			}
		}
	}
	if targetPtr, ok := mp["target"]; ok && targetPtr != nil {
		if target, ok := (*targetPtr).(string); ok {
			if !tmpObj.TargetOutputed {
				fmt.Printf("Edit target: %s\n", target)
				tmpObj.TargetOutputed = true
			}
		}
	}
	if textPtr, ok := mp["text"]; ok && textPtr != nil {
		var textOut string
		if text, ok := (*textPtr).(string); ok {
			textOut = text
		}
		if text, ok := (*textPtr).(json.StringSlot); ok {
			textOut = string(text)
		}
		if textOut != "" && int(tmpObj.TextOutputedLen) == 0 {
			fmt.Print("Edit text: ")
		}
		if textOut != "" && int(tmpObj.TextOutputedLen) < len(textOut) {
			fmt.Print(textOut[tmpObj.TextOutputedLen:])
			tmpObj.TextOutputedLen = int32(len(textOut))
		}
	}
	session.TemporyDataOfRequest["tools:edit"] = tmpObj
	return true, cross, nil
}

// CheckPath 处理路径
func CheckPath(mp map[string]*any) (string, error) {
	// 检查并获取path参数
	pathPtr, ok := mp["path"]
	if !ok || pathPtr == nil {
		return "", errors.New("missing path parameter")
	}
	path, ok := (*pathPtr).(string)
	if !ok || path == "" {
		return "", errors.New("invalid or empty path parameter")
	}
	// 检查path
	if strings.Contains(path, "..") {
		return "", errors.New("path cannot contains '..'")
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
		strings.Contains(path, "\t") ||
		strings.Contains(path, "..") {
		return "", errors.New("path must be a correct and relative path")
	}
	return path, nil
}

// CheckTargetText 处理目标和文本
func CheckTargetText(mp map[string]*any) (string, string, error) {
	// 检查并获取target参数
	targetPtr, ok := mp["target"]
	if !ok || targetPtr == nil {
		return "", "", errors.New("missing target parameter")
	}
	target, ok := (*targetPtr).(string)
	if !ok {
		return "", "", errors.New("invalid target parameter")
	}

	// 检查并获取text参数
	textPtr, ok := mp["text"]
	if !ok || textPtr == nil {
		return "", "", errors.New("missing text parameter")
	}
	text, ok := (*textPtr).(string)
	if !ok {
		return "", "", errors.New("invalid text parameter")
	}

	return target, text, nil
}

// ProcessString 执行字符串编辑
func ProcessString(content, target, text string, fileExists bool) (string, error) {
	var newContent string
	var err error

	// 根据target执行不同的编辑操作
	switch {
	case target == "":
		// 追加到文件末尾
		if fileExists {
			if content != "" && !strings.HasSuffix(content, "\n") {
				newContent = content + "\n" + text + "\n"
			} else {
				newContent = content + text + "\n"
			}
		} else {
			newContent = text + "\n"
		}

	case target == "@all":
		// 替换整个文件
		newContent = text + "\n"

	case strings.HasPrefix(target, "@ln:"):
		lines := strings.Split(content, "\n")
		newContent, err = handleLineReplace(lines, target, text)
		if err != nil {
			return "", err
		}
	case strings.HasPrefix(target, "@insert:"):
		lines := strings.Split(content, "\n")
		newContent, err = handleLineInsert(lines, target, text)
		if err != nil {
			return "", err
		}

	case strings.HasPrefix(target, "@regex:"):
		newContent, err = handleRegexEdit(content, target, text)
		return newContent, err

	default:
		// 替换第一个匹配的子字符串
		if !fileExists {
			return "", errors.New("file does not exist, cannot replace substring")
		}
		if !strings.Contains(content, target) {
			return "", fmt.Errorf("target string not found: %s", target)
		}
		newContent = strings.Replace(content, target, text, 1)
	}
	return newContent, nil
}

func writeFile(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	path, err := CheckPath(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	target, text, err := CheckTargetText(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	path = filepath.Join(session.CurrentActivatePath, path)

	// 读取文件内容
	var content string
	lines := []string{}
	fileExists := true

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fileExists = false
		} else {
			boolx := false
			success := any(boolx)
			errMsg := any(fmt.Sprintf("failed to open file: %v", err))
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
	} else {
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any(fmt.Sprintf("failed to read file: %v", err))
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		content = strings.Join(lines, "\n")
	}

	logger.Info("edit file \"%s\" mode \"%s\" in ID=%d,agentID=%s", path, target, session.ID, session.CurrentAgentID)
	newContent, err := ProcessString(content, target, text, fileExists)
	if err != nil {
		logger.Warn("failed to process string: %v", err)
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 写入文件
	err = os.WriteFile(path, []byte(newContent), 0644)
	if err != nil {
		logger.Warn("failed to write file: %v", err)
		boolx := false
		success := any(boolx)
		errMsg := any(fmt.Sprintf("failed to write file: %v", err))
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	pathStr := any("")
	trace.Trace(session, map[string]*any{
		"path": &pathStr,
	}, []*any{})

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

func handleLineReplace(lines []string, target, text string) (string, error) {
	parts := strings.TrimPrefix(target, "@ln:")

	if !strings.Contains(parts, "-") {
		lineNum, err := strconv.Atoi(parts)

		if err != nil {
			return "", fmt.Errorf("invalid line number: %s", parts)
		}

		from := lineNum
		to := lineNum

		if lineNum > len(lines) {
			return "", fmt.Errorf("line %d exceeds file length %d", from, len(lines))
		}
		// 构建新内容
		var buf bytes.Buffer
		for i := 0; i < from-1; i++ {
			buf.WriteString(lines[i] + "\n")
		}
		buf.WriteString(text + "\n")
		for i := to; i < len(lines); i++ {
			buf.WriteString(lines[i] + "\n")
		}

		return buf.String(), nil
	}

	// @ln:{from}-{to} 替换行范围
	rangeParts := strings.Split(parts, "-")

	if len(rangeParts) != 2 {
		return "", fmt.Errorf("invalid line range: %s", parts)
	}

	from, err := strconv.Atoi(rangeParts[0])

	if err != nil {
		return "", fmt.Errorf("invalid line number: %s", rangeParts[0])
	}

	to, err := strconv.Atoi(rangeParts[1])

	if err != nil {
		return "", fmt.Errorf("invalid line number: %s", rangeParts[1])
	}

	if from > len(lines) {
		return "", fmt.Errorf("from line %d exceeds file length %d", from, len(lines))
	}
	if to > len(lines) {
		return "", fmt.Errorf("to line %d exceeds file length %d", to, len(lines))
	}

	// 构建新内容
	var buf bytes.Buffer
	for i := 0; i < from-1; i++ {
		buf.WriteString(lines[i] + "\n")
	}
	buf.WriteString(text + "\n")
	for i := to; i < len(lines); i++ {
		buf.WriteString(lines[i] + "\n")
	}

	return buf.String(), nil

}
func handleLineInsert(lines []string, target, text string) (string, error) {
	parts := strings.TrimPrefix(target, "@insert:")

	lineNum, err := strconv.Atoi(parts)

	if err != nil {
		return "", fmt.Errorf("invalid line number: %s", parts)
	}

	if lineNum > len(lines) {
		return "", fmt.Errorf("line %d exceeds file length %d", lineNum, len(lines))
	}

	// 构建新内容
	var buf bytes.Buffer
	for i := range lineNum - 1 {
		buf.WriteString(lines[i] + "\n")
	}
	buf.WriteString(text + "\n")
	for i := lineNum - 1; i < len(lines); i++ {
		buf.WriteString(lines[i] + "\n")
	}

	return buf.String(), nil
}

func handleRegexEdit(content, target, text string) (string, error) {
	// 解析: @regex:/pattern/flags
	patternPart := strings.TrimPrefix(target, "@regex:")

	if len(patternPart) < 3 || patternPart[0] != '/' {
		return "", fmt.Errorf("invalid regex format, expected @regex:/pattern/flags")
	}

	// 去掉开头的'/'
	patternPart = patternPart[1:]

	// 找到最后一个/来分隔pattern和flags
	lastSlash := strings.LastIndex(patternPart, "/")
	if lastSlash < 0 {
		return "", fmt.Errorf("invalid regex format, missing closing /")
	}

	pattern := patternPart[:lastSlash]
	flags := ""
	if lastSlash+1 < len(patternPart) {
		flags = patternPart[lastSlash+1:]
	}

	if pattern == "" {
		return "", fmt.Errorf("empty regex pattern")
	}

	// 检查是否找到匹配
	var re *regexp.Regexp
	var err error

	if strings.Contains(flags, "i") {
		re, err = regexp.Compile("(?i)" + pattern)
	} else {
		re, err = regexp.Compile(pattern)
	}

	if err != nil {
		return "", fmt.Errorf("invalid regex pattern '%s': %v", pattern, err)
	}

	// 检查是否有匹配
	matches := re.FindAllString(content, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("regex pattern '%s' not found in file", pattern)
	}

	// 执行替换
	global := strings.Contains(flags, "g")

	if global {
		newContent := re.ReplaceAllString(content, text)
		return newContent, nil
	}
	newContent := re.ReplaceAllString(content, text)
	return newContent, nil
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
