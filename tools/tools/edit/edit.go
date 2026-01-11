package edit

import (
	"bufio"
	"bytes"
	_ "embed" // embed
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/values"
)

const toolName = "edit"

//go:embed prompt.md
var prompt string

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

func buildPrompt() (string, error) {
	return prompt, nil
}

func updateInfo(mp map[string]*any, cross []*any) (bool, []*any, error) {
	// 只在参数存在时输出，支持流式更新
	if pathPtr, ok := mp["path"]; ok && pathPtr != nil {
		if path, ok := (*pathPtr).(string); ok {
			fmt.Printf("Edit path: %s\n", path)
		}
	}
	if targetPtr, ok := mp["target"]; ok && targetPtr != nil {
		if target, ok := (*targetPtr).(string); ok {
			fmt.Printf("Edit target: %s\n", target)
		}
	}
	if textPtr, ok := mp["text"]; ok && textPtr != nil {
		if text, ok := (*textPtr).(string); ok {
			// 限制text输出长度，避免日志过多
			if len(text) > 100 {
				fmt.Printf("Edit text: %s... (truncated)\n", text[:100])
			} else {
				fmt.Printf("Edit text: %s\n", text)
			}
		}
	}
	return true, cross, nil
}

func writeFile(mp map[string]*any, push []*any) (bool, []*any, map[string]*any, error) {
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

	// 检查并获取target参数
	targetPtr, ok := mp["target"]
	if !ok || targetPtr == nil {
		boolx := false
		success := any(boolx)
		errMsg := any("missing target parameter")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	target, ok := (*targetPtr).(string)
	if !ok {
		boolx := false
		success := any(boolx)
		errMsg := any("invalid target parameter")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 检查并获取text参数
	textPtr, ok := mp["text"]
	if !ok || textPtr == nil {
		boolx := false
		success := any(boolx)
		errMsg := any("missing text parameter")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	text, ok := (*textPtr).(string)
	if !ok {
		boolx := false
		success := any(boolx)
		errMsg := any("invalid text parameter")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	path = filepath.Join(values.CurrentActivatePath, path)

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
			return false, push, map[string]*any{
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
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		content = strings.Join(lines, "\n")
	}

	var newContent string

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
		newContent, err = handleLineEdit(lines, target, text)
		if err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}

	case strings.HasPrefix(target, "@regex:"):
		newContent, err = handleRegexEdit(content, target, text)
		if err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}

	default:
		// 替换第一个匹配的子字符串
		if !fileExists {
			boolx := false
			success := any(boolx)
			errMsg := any("file does not exist, cannot replace substring")
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		if !strings.Contains(content, target) {
			boolx := false
			success := any(boolx)
			errMsg := any(fmt.Sprintf("target string not found: %s", target))
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		newContent = strings.Replace(content, target, text, 1)
	}

	// 写入文件
	err = os.WriteFile(path, []byte(newContent), 0644)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(fmt.Sprintf("failed to write file: %v", err))
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	boolx := true
	success := any(boolx)
	return false, push, map[string]*any{
		"success": &success,
	}, nil
}

func handleLineEdit(lines []string, target, text string) (string, error) {
	parts := strings.TrimPrefix(target, "@ln:")

	if strings.Contains(parts, "-") {
		// @ln:{from}-{to} 替换行范围
		rangeParts := strings.Split(parts, "-")
		from, _ := strconv.Atoi(rangeParts[0])
		to, _ := strconv.Atoi(rangeParts[1])

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
	// @ln:{line} 在指定行后插入
	lineNum, _ := strconv.Atoi(parts)

	if lineNum > len(lines) {
		return "", fmt.Errorf("line %d exceeds file length %d", lineNum, len(lines))
	}

	// 构建新内容
	var buf bytes.Buffer
	for i := range lineNum {
		buf.WriteString(lines[i] + "\n")
	}
	buf.WriteString(text + "\n")
	for i := lineNum; i < len(lines); i++ {
		buf.WriteString(lines[i] + "\n")
	}

	return buf.String(), nil

}

func handleRegexEdit(content, target, text string) (string, error) {
	// 解析新格式: @regex:/pattern/flags
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
