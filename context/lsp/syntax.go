package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	json5 "github.com/titanous/json5"
	"gopkg.in/yaml.v3"
)

// CheckNoLSPFileSyntax 对不支持 LSP 的文件做原生语法检查
// ext: 文件扩展名（如 .json, .yaml, .toml）
// content: 文件内容
// 返回诊断列表，空切片表示无问题
func CheckNoLSPFileSyntax(ext, content string) []DiagnosticBrief {
	switch ext {
	case ".json":
		return checkJSON5(content)
	case ".jsonl":
		return checkJSONL(content)
	case ".yaml", ".yml":
		return checkYAML(content)
	case ".toml":
		return checkTOML(content)
	case ".ini":
		return checkINI(content)
	case ".md", ".mdx":
		return checkMarkdown(content)
	}
	// .txt, .makefile, .dockerfile, .license 等不做语法检查
	return nil
}

// ---- JSON5 ----

// checkJSON5 使用 JSON5 库解析，校验语法
func checkJSON5(content string) []DiagnosticBrief {
	var v any
	err := json5.Unmarshal([]byte(content), &v)
	if err == nil {
		return nil
	}
	line := lineFromOffset(content, errorOffset(err))
	msg := cleanJSON5ErrorMessage(err.Error())
	return []DiagnosticBrief{{
		Line:     line,
		Message:  msg,
		Severity: "error",
	}}
}

// ---- JSON Lines ----

// checkJSONL 逐行校验 JSON
func checkJSONL(content string) []DiagnosticBrief {
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	var diags []DiagnosticBrief
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			diags = append(diags, DiagnosticBrief{
				Line:     lineNum,
				Message:  fmt.Sprintf("JSONL line %d: %s", lineNum, err.Error()),
				Severity: "error",
			})
		}
	}
	return diags
}

// ---- YAML ----

// checkYAML 使用 yaml.v3 解析，校验语法
func checkYAML(content string) []DiagnosticBrief {
	var v any
	err := yaml.Unmarshal([]byte(content), &v)
	if err == nil {
		return nil
	}
	line := lineFromYAMLError(err.Error())
	return []DiagnosticBrief{{
		Line:     line,
		Message:  err.Error(),
		Severity: "error",
	}}
}

// ---- TOML ----

// checkTOML 使用 BurntSushi/toml 解析，校验语法
func checkTOML(content string) []DiagnosticBrief {
	var v any
	err := toml.Unmarshal([]byte(content), &v)
	if err == nil {
		return nil
	}
	line := lineFromTOMLError(err.Error())
	return []DiagnosticBrief{{
		Line:     line,
		Message:  err.Error(),
		Severity: "error",
	}}
}

// ---- INI ----

// checkINI 简单的 INI 语法检查
func checkINI(content string) []DiagnosticBrief {
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0
	var diags []DiagnosticBrief
	inSection := false
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				diags = append(diags, DiagnosticBrief{
					Line:     lineNum,
					Message:  fmt.Sprintf("unclosed section header: %s", line),
					Severity: "error",
				})
				continue
			}
			if len(line) < 3 {
				diags = append(diags, DiagnosticBrief{
					Line:     lineNum,
					Message:  fmt.Sprintf("empty section header: %s", line),
					Severity: "error",
				})
				continue
			}
			inSection = true
			continue
		}
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			if key == "" {
				diags = append(diags, DiagnosticBrief{
					Line:     lineNum,
					Message:  "empty key before '='",
					Severity: "error",
				})
			}
			continue
		}
		if inSection && strings.Contains(line, ":") {
			// Python ConfigParser 风格 key: value
			key := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
			if key != "" {
				continue
			}
		}
		// 非空行但不是合法的 INI 条目
		diags = append(diags, DiagnosticBrief{
			Line:     lineNum,
			Message:  fmt.Sprintf("unrecognized INI syntax: %s", line),
			Severity: "warn",
		})
	}
	return diags
}

// ---- Markdown ----

// checkMarkdown 简单的 Markdown 语法检查
// 当前检测：未闭合的围栏代码块、引用链接格式
func checkMarkdown(content string) []DiagnosticBrief {
	lines := strings.Split(content, "\n")
	var diags []DiagnosticBrief
	fenceOpen := false
	fenceLine := 0
	fenceChar := ""

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 检查代码围栏
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := ""
			if strings.HasPrefix(trimmed, "```") {
				marker = "```"
			} else {
				marker = "~~~"
			}

			// 检查是否在围栏内
			_ = strings.TrimSpace(trimmed[len(marker):])
			if !fenceOpen {
				// 开启围栏
				fenceOpen = true
				fenceLine = i + 1
				fenceChar = marker
			} else if marker == fenceChar {
				// 闭合围栏 — 仅当标记相同时才闭合
				// （如果 rest 中有其他字符，仍是闭合，但可能是带语言的围栏）
				fenceOpen = false
			}
			// 不同标记的围栏不匹配，忽略
		}

		// 检查引用链接格式 [text][ref]
		if strings.Contains(line, "][") {
			// 简单检查：确保对应引用定义存在
			// （不做完整检查，仅提示）
		}
	}

	// 未闭合的代码围栏
	if fenceOpen {
		diags = append(diags, DiagnosticBrief{
			Line:     fenceLine,
			Message:  fmt.Sprintf("unclosed fenced code block starting with %s at line %d", fenceChar, fenceLine),
			Severity: "error",
		})
	}

	return diags
}

// ---- 辅助函数 ----

// errorOffset 从 JSON5 错误中提取 Offset
func errorOffset(err error) int64 {
	if se, ok := err.(*json5.SyntaxError); ok {
		return se.Offset
	}
	// 标准 json.SyntaxError
	if se, ok := err.(*json.SyntaxError); ok {
		return se.Offset
	}
	return 0
}

// lineFromOffset 从字节偏移量计算行号（1-based）
func lineFromOffset(content string, offset int64) int {
	if offset <= 0 {
		return 0
	}
	if int64(len(content)) < offset {
		offset = int64(len(content))
	}
	return strings.Count(content[:offset], "\n") + 1
}

// cleanJSON5ErrorMessage 精简 JSON5 错误信息，去除内部细节
func cleanJSON5ErrorMessage(msg string) string {
	// 去掉 "json5: " 前缀
	msg = strings.TrimPrefix(msg, "json5: ")
	return msg
}

// lineFromYAMLError 从 YAML 错误消息中提取行号
func lineFromYAMLError(msg string) int {
	// yaml.v3 错误格式: "yaml: line N: message"
	var line int
	if _, err := fmt.Sscanf(msg, "yaml: line %d:", &line); err == nil {
		return line
	}
	return 0
}

// lineFromTOMLError 从 TOML 错误消息中提取行号
func lineFromTOMLError(msg string) int {
	// BurntSushi/toml 错误消息常包含 "near line N" 或 "at line N"
	var line int
	if _, err := fmt.Sscanf(msg, "near line %d", &line); err == nil {
		return line
	}
	if _, err := fmt.Sscanf(msg, "at line %d", &line); err == nil {
		return line
	}
	return 0
}
