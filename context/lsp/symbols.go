package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GetSymbols 获取指定文件的所有顶层符号及其结构体成员和注释
func (m *Manager) GetSymbols(workdir, filePath string) ([]SymbolResult, error) {
	// 获取或创建 client
	client, err := m.getClient(workdir, filePath)
	if err != nil {
		return nil, fmt.Errorf("lsp get client: %w", err)
	}

	// 读取文件内容
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	text := string(content)

	// 构建 URI
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("abs path: %w", err)
	}
	uri := pathToURI(absPath)

	// 获取扩展名和语言 ID
	ext := extFromPath(filePath)
	langID := languageIDFromExt(ext)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 发送 didOpen
	if err := client.SendNotification("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: langID,
			Version:    1,
			Text:       text,
		},
	}); err != nil {
		return nil, fmt.Errorf("didOpen: %w", err)
	}

	// 确保发送 didClose
	defer func() {
		_ = client.SendNotification("textDocument/didClose", DidCloseTextDocumentParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
		})
	}()

	// 发送 documentSymbol 请求
	raw, err := client.SendRequest(ctx, "textDocument/documentSymbol", DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	})
	if err != nil {
		return nil, fmt.Errorf("documentSymbol: %w", err)
	}

	if raw == nil {
		return nil, nil
	}

	// 解析 documentSymbol 响应
	symbols, err := parseDocumentSymbols(raw)
	if err != nil {
		return nil, fmt.Errorf("parse symbols: %w", err)
	}

	// 提取顶层符号
	var results []SymbolResult
	for _, sym := range symbols {
		if !isTopLevel(sym.Kind) {
			continue
		}
		result := m.buildSymbolResult(ctx, client, text, uri, sym)
		results = append(results, result)
	}

	return results, nil
}

// buildSymbolResult 构建单个符号的结果（含注释、代码和成员）
func (m *Manager) buildSymbolResult(ctx context.Context, client *Client, text, uri string, sym DocumentSymbol) SymbolResult {
	result := SymbolResult{
		Name:     sym.Name,
		Kind:     sym.Kind,
		KindName: SymbolKindNames[sym.Kind],
		Detail:   sym.Detail,
		Line:     sym.Range.Start.Line,
	}

	// 获取 hover 签名+文档注释（合在一起）
	signature, docComment := m.getHoverInfo(ctx, client, uri, sym.SelectionRange.Start)
	result.Signature = signature
	if docComment != "" {
		result.Signature += "\n" + docComment
	}
	result.DocComment = formatDocComment(docComment, languageFromURI(uri))

	// 提取完整代码
	result.Code = extractFullCode(text, sym.Range)

	return result
}

// getHoverInfo 通过 hover 获取符号的签名和文档注释
func (m *Manager) getHoverInfo(ctx context.Context, client *Client, uri string, position Position) (signature, docComment string) {
	hoverData, err := client.SendRequest(ctx, "textDocument/hover", HoverParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
	if err != nil || hoverData == nil {
		return "", ""
	}

	return extractHoverInfo(hoverData)
}

// ---------------------------------------------------------------------------
// 解析辅助函数
// ---------------------------------------------------------------------------

// parseDocumentSymbols 解析 documentSymbol 响应
// LSP 返回类型可以是 []DocumentSymbol（层级结构）或 []SymbolInformation（扁平结构）
func parseDocumentSymbols(raw json.RawMessage) ([]DocumentSymbol, error) {
	// 先尝试解析为 json.RawMessage 数组
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("not an array: %w", err)
	}

	var symbols []DocumentSymbol
	for _, item := range arr {
		sym, err := parseOneSymbol(item)
		if err != nil {
			logger.Warn("skip symbol parse error: %v", err)
			continue
		}
		if sym != nil {
			symbols = append(symbols, *sym)
		}
	}
	return symbols, nil
}

// parseOneSymbol 解析单个 DocumentSymbol
func parseOneSymbol(raw json.RawMessage) (*DocumentSymbol, error) {
	// 探测是 DocumentSymbol（有 range）还是 SymbolInformation（有 location）
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}

	if _, hasRange := probe["range"]; hasRange {
		// DocumentSymbol 格式
		var sym DocumentSymbol
		if err := json.Unmarshal(raw, &sym); err != nil {
			return nil, err
		}
		if sym.Children == nil {
			sym.Children = make([]DocumentSymbol, 0)
		}
		return &sym, nil
	}

	// SymbolInformation 格式（扁平结构，有 location 字段）
	// LSP 也返回这种格式，我们转换成 DocumentSymbol
	type symbolInfo struct {
		Name     string     `json:"name"`
		Kind     SymbolKind `json:"kind"`
		Location struct {
			URI   string `json:"uri"`
			Range Range  `json:"range"`
		} `json:"location"`
		ContainerName string `json:"containerName,omitempty"`
	}
	var info symbolInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}

	sym := &DocumentSymbol{
		Name:  info.Name,
		Kind:  info.Kind,
		Range: info.Location.Range,
		SelectionRange: Range{
			Start: info.Location.Range.Start,
			End:   Position{Line: info.Location.Range.Start.Line, Character: info.Location.Range.Start.Character + uint32(len(info.Name))},
		},
		Children: make([]DocumentSymbol, 0),
	}
	return sym, nil
}

// extractHoverInfo 从 hover 响应中提取签名和文档注释
// LSP hover 返回格式通常为 markdown，签名在第一段代码块中
func extractHoverInfo(data json.RawMessage) (signature, docComment string) {
	// 解析顶层
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		return "", ""
	}

	contents, ok := top["contents"]
	if !ok || contents == nil {
		return "", ""
	}

	var rawText string
	switch c := contents.(type) {
	case string:
		rawText = c
	case map[string]any:
		if value, ok := c["value"]; ok {
			if s, ok := value.(string); ok {
				rawText = s
			}
		}
	case []any:
		// 某些 LSP 返回 MarkedString 数组
		var parts []string
		for _, item := range c {
			switch it := item.(type) {
			case string:
				parts = append(parts, it)
			case map[string]any:
				if value, ok := it["value"]; ok {
					if s, ok := value.(string); ok {
						parts = append(parts, s)
					}
				}
			}
		}
		rawText = strings.Join(parts, "\n")
	}

	if rawText == "" {
		return "", ""
	}

	return splitSignatureAndDoc(rawText)
}

// splitSignatureAndDoc 将 hover 文本拆分为签名和文档注释
// markdown 模式：第一个 ```...``` 代码块为签名，其余为文档
// plaintext 模式：空行前为签名，空行后为文档
func splitSignatureAndDoc(text string) (signature, docComment string) {
	lines := strings.Split(text, "\n")

	// 检查是否以代码块开头（markdown 模式）
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		var sigLines []string
		i := 1
		for ; i < len(lines); i++ {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				i++ // 跳过关闭的 ```
				break
			}
			sigLines = append(sigLines, lines[i])
		}
		signature = strings.TrimSpace(strings.Join(sigLines, "\n"))
		docComment = strings.TrimSpace(strings.Join(lines[i:], "\n"))
		return
	}

	// plaintext 模式：第一个 block（空行前）为签名
	var sigLines []string
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			signature = strings.TrimSpace(strings.Join(sigLines, "\n"))
			docComment = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			return
		}
		sigLines = append(sigLines, line)
	}

	// 只有一行或没有空行分隔，全部作为签名
	return strings.TrimSpace(text), ""
}

// formatDocComment 根据语言格式化文档注释
// Go: // comment
// Python: """comment"""
// 其他: # comment
func formatDocComment(comment string, language string) string {
	if comment == "" {
		return ""
	}

	lines := strings.Split(comment, "\n")
	var formatted []string

	commentLang := strings.ToLower(language)
	switch commentLang {
	case "go":
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				formatted = append(formatted, "//")
			} else {
				formatted = append(formatted, "// "+line)
			}
		}
	case "python":
		if len(lines) == 1 {
			return `"""` + lines[0] + `"""`
		}
		formatted = append(formatted, `"""`)
		for _, line := range lines {
			formatted = append(formatted, line)
		}
		formatted = append(formatted, `"""`)
	case "c", "cpp", "java", "kotlin", "csharp", "rust", "typescript", "javascript":
		if len(lines) == 1 {
			formatted = append(formatted, "// "+lines[0])
		} else {
			formatted = append(formatted, "/*")
			for _, line := range lines {
				if strings.TrimSpace(line) == "" {
					formatted = append(formatted, " *")
				} else {
					formatted = append(formatted, " * "+line)
				}
			}
			formatted = append(formatted, " */")
		}
	default:
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				formatted = append(formatted, "#")
			} else {
				formatted = append(formatted, "# "+line)
			}
		}
	}

	return strings.Join(formatted, "\n")
}

// extractFullCode 从文件内容中提取符号的完整代码（含函数体/结构体成员等）
func extractFullCode(content string, rng Range) string {
	lines := strings.Split(content, "\n")
	if int(rng.Start.Line) < 0 || int(rng.Start.Line) >= len(lines) {
		return ""
	}

	endLine := int(rng.End.Line)
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}

	var codeLines []string
	for i := int(rng.Start.Line); i <= endLine; i++ {
		codeLines = append(codeLines, lines[i])
	}

	return strings.TrimRight(strings.Join(codeLines, "\n"), " \t\n\r")
}

// ---------------------------------------------------------------------------
// URI 处理
// ---------------------------------------------------------------------------

// pathToURI 将文件路径转为 file:// URI
func pathToURI(absPath string) string {
	// Linux: file:///path/to/file
	return "file://" + absPath
}

// languageFromURI 从 URI 推断语言
// 实际上从 extFromPath 获取更可靠，这里作为备选
func languageFromURI(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		path := strings.TrimPrefix(uri, "file://")
		ext := extFromPath(path)
		return languageIDFromExt(ext)
	}
	return ""
}
