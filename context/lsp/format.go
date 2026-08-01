package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/log"
)

var formatLogger = log.New("lsp:format")

// FormatResult 格式化+诊断结果
type FormatResult struct {
	Formatted   bool              `json:"formatted"`
	Diagnostics []DiagnosticBrief `json:"diagnostics,omitempty"`
	Error       string            `json:"error,omitempty"`
}

// DiagnosticBrief 诊断信息（供 AI 消费）
// 包含行号、列号、消息、严重级别、来源（LSP 服务器名）和错误码
type DiagnosticBrief struct {
	Line     int    `json:"line"`
	Column   int    `json:"column,omitempty"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Source   string `json:"source,omitempty"`
	Code     string `json:"code,omitempty"`
}

// FormatAndDiagnose 格式化文件并获取诊断信息
// workdir: LSP 工作目录（通常为 session.Root）
// filePath: 文件绝对路径
// 对支持 LSP 的语言：调用 LSP 格式化 + publishDiagnostics
// 对不支持 LSP 的文件（JSON/YAML/TOML/INI/Markdown 等）：做原生语法检查
// 即使 LSP 被禁用，原生语法检查仍然会运行
func FormatAndDiagnose(workdir, filePath string) *FormatResult {
	ext := extFromPath(filePath)

	// 读取文件内容（两种路径都需要）
	content, err := os.ReadFile(filePath)
	if err != nil {
		return &FormatResult{Error: fmt.Sprintf("read file: %v", err)}
	}
	text := string(content)

	// 检查是否需要 LSP
	if _, err := resolveLanguageServer(ext); err != nil {
		// 不支持 LSP 或 LSP 被禁用 — 做原生语法检查即可
		formatLogger.Debug("no LSP for %s, native syntax check", ext)
		result := &FormatResult{}
		result.Diagnostics = CheckNoLSPFileSyntax(ext, text)
		return result
	}

	// 需要 LSP 但 manager 未初始化（LSP 被禁用）
	if globalManager == nil {
		formatLogger.Debug("LSP disabled but %s requires LSP, skipping", ext)
		return &FormatResult{}
	}

	return globalManager.FormatAndDiagnose(workdir, filePath)
}

// FormatAndDiagnose 格式化文件并获取诊断
// 对支持 LSP 的语言：调用 LSP 格式化 + publishDiagnostics
// 对不支持 LSP 的文件（JSON/YAML/TOML/INI/Markdown 等）：做原生语法检查
func (m *Manager) FormatAndDiagnose(workdir, filePath string) *FormatResult {
	ext := extFromPath(filePath)
	result := &FormatResult{}

	// 读取文件内容
	content, err := os.ReadFile(filePath)
	if err != nil {
		return &FormatResult{Error: fmt.Sprintf("read file: %v", err)}
	}
	text := string(content)

	// 检查是否有 LSP 支持
	if _, err := resolveLanguageServer(ext); err != nil {
		// 不支持 LSP — 做原生语法检查
		formatLogger.Debug("no LSP for %s, native syntax check", ext)
		result.Diagnostics = CheckNoLSPFileSyntax(ext, text)
		return result
	}

	// 获取或创建 LSP client
	client, err := m.getClient(workdir, filePath)
	if err != nil {
		return &FormatResult{Error: fmt.Sprintf("get LSP client: %v", err)}
	}

	// 构建 URI
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return &FormatResult{Error: fmt.Sprintf("abs path: %v", err)}
	}
	uri := pathToURI(absPath)
	langID := languageIDFromExt(ext)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 发送 didOpen 通知 LSP 服务器
	if err := client.SendNotification("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: langID,
			Version:    1,
			Text:       text,
		},
	}); err != nil {
		return &FormatResult{Error: fmt.Sprintf("didOpen: %v", err)}
	}

	// 确保关闭文档
	defer func() {
		_ = client.SendNotification("textDocument/didClose", DidCloseTextDocumentParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
		})
	}()

	// --- 格式化 ---
	formatRaw, err := client.SendRequest(ctx, "textDocument/formatting", DocumentFormattingParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Options: FormattingOptions{
			TabSize:      4,
			InsertSpaces: false,
		},
	})

	if err == nil && formatRaw != nil {
		var edits []TextEdit
		if err := json.Unmarshal(formatRaw, &edits); err == nil && len(edits) > 0 {
			newText := applyTextEdits(text, edits)
			if newText != text {
				// 格式后有变化，写回磁盘
				if writeErr := os.WriteFile(filePath, []byte(newText), 0644); writeErr == nil {
					result.Formatted = true
					text = newText // 更新 text，后续诊断基于格式化后的内容
					formatLogger.Info("formatted: %s", filePath)
				} else {
					formatLogger.Warn("write formatted file: %v", writeErr)
				}
			}
		}
	}

	// --- 诊断 ---
	// 设置诊断通知回调（仅捕获本次 URI 的诊断）
	diagCh := make(chan []Diagnostic, 1)
	handler := func(method string, params json.RawMessage) {
		if method == "textDocument/publishDiagnostics" {
			var p PublishDiagnosticsParams
			if err := json.Unmarshal(params, &p); err == nil && p.URI == uri {
				select {
				case diagCh <- p.Diagnostics:
				default:
				}
			}
		}
	}

	// 注册通知处理器（保留原处理器链）。
	// 在锁内一次性读旧值+写新值，避免并发 FormatAndDiagnose 互相覆盖/错误恢复。
	client.transport.notifMu.Lock()
	oldHandler := client.transport.notifHandler
	client.transport.notifHandler = func(method string, params json.RawMessage) {
		handler(method, params)
		if oldHandler != nil {
			oldHandler(method, params)
		}
	}
	client.transport.notifMu.Unlock()
	defer func() {
		client.transport.notifMu.Lock()
		client.transport.notifHandler = oldHandler
		client.transport.notifMu.Unlock()
	}()

	// 如果格式化了，发送 didChange 同步最新内容给 LSP 服务器触发诊断
	if result.Formatted {
		_ = client.SendNotification("textDocument/didChange", DidChangeTextDocumentParams{
			TextDocument: VersionedTextDocumentIdentifier{
				URI:     uri,
				Version: 2,
			},
			ContentChanges: []TextDocumentContentChangeEvent{
				{Text: text},
			},
		})
	}

	// 等待诊断结果（短暂超时）
	select {
	case diags := <-diagCh:
		result.Diagnostics = make([]DiagnosticBrief, 0, len(diags))
		for _, d := range diags {
			code := ""
			if d.Code != nil {
				code = fmt.Sprintf("%v", d.Code)
			}
			result.Diagnostics = append(result.Diagnostics, DiagnosticBrief{
				Line:     int(d.Range.Start.Line) + 1, // LSP 行号从 0 开始，转成 1-based
				Column:   int(d.Range.Start.Character) + 1,
				Message:  d.Message,
				Severity: severityName(d.Severity),
				Source:   d.Source,
				Code:     code,
			})
		}
	case <-time.After(3 * time.Second):
		// 超时，无诊断
	}

	return result
}

// applyTextEdits 按行应用 TextEdit 列表到原始文本
// TextEdit 按 Range.Start 逆序应用（从后往前），避免位置偏移
func applyTextEdits(text string, edits []TextEdit) string {
	if len(edits) == 0 {
		return text
	}

	// 按起始行逆序排序（从后往前应用，确保位置正确）
	sorted := make([]TextEdit, len(edits))
	copy(sorted, edits)
	sort.Slice(sorted, func(i, j int) bool {
		si, sj := sorted[i].Range.Start, sorted[j].Range.Start
		if si.Line != sj.Line {
			return si.Line > sj.Line
		}
		return si.Character > sj.Character
	})

	lines := splitLines(text)
	for _, edit := range sorted {
		startLine, endLine := int(edit.Range.Start.Line), int(edit.Range.End.Line)
		startChar := int(edit.Range.Start.Character)
		endChar := int(edit.Range.End.Character)

		// 确保行号有效
		if startLine < 0 || startLine >= len(lines) {
			continue
		}
		if endLine < 0 || endLine >= len(lines) {
			endLine = len(lines) - 1
		}

		// 构建替换后的行序列
		var newLines []string

		// 替换范围前的行
		for i := range startLine {
			newLines = append(newLines, lines[i])
		}

		// 替换起始行之前的部分 + newText + 替换结束行之后的部分
		prefix := ""
		if startChar > 0 && startChar <= len(lines[startLine]) {
			prefix = lines[startLine][:startChar]
		}
		suffix := ""
		if endChar > 0 && endChar <= len(lines[endLine]) {
			suffix = lines[endLine][endChar:]
		}

		// newText 可能包含多行
		editLines := splitLines(edit.NewText)
		if len(editLines) == 0 {
			newLines = append(newLines, prefix+suffix)
		} else if len(editLines) == 1 {
			// 单行编辑：prefix + NewText + suffix（必须补 suffix，否则丢失行尾内容）
			newLines = append(newLines, prefix+editLines[0]+suffix)
		} else {
			// 第一行：prefix + 编辑文本首行
			newLines = append(newLines, prefix+editLines[0])
			// 中间行
			for i := 1; i < len(editLines)-1; i++ {
				newLines = append(newLines, editLines[i])
			}
			// 最后一行：编辑文本末行 + suffix
			newLines = append(newLines, editLines[len(editLines)-1]+suffix)
		}

		// 替换范围后的行
		for i := endLine + 1; i < len(lines); i++ {
			newLines = append(newLines, lines[i])
		}

		lines = newLines
	}

	return joinLines(lines)
}

// splitLines 按 \n 分割字符串，保留空行
func splitLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

// joinLines 将行合并为字符串，每行加 \n
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var result strings.Builder
	for _, line := range lines[:len(lines)-1] {
		result.WriteString(line + "\n")
	}
	result.WriteString(lines[len(lines)-1])
	return result.String()
}

// severityName 诊断严重程度中文名
func severityName(s DiagnosticSeverity) string {
	switch s {
	case DiagnosticSeverityError:
		return "error"
	case DiagnosticSeverityWarning:
		return "warning"
	case DiagnosticSeverityInformation:
		return "info"
	case DiagnosticSeverityHint:
		return "hint"
	default:
		return "unknown"
	}
}
