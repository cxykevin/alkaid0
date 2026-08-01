package lsp

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 协议类型
// ---------------------------------------------------------------------------

// jsonrpcRequest JSON-RPC 2.0 请求
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonrpcResponse JSON-RPC 2.0 响应
type jsonrpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      int64     `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

// jsonrpcNotification JSON-RPC 2.0 通知（无 ID）
type jsonrpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcError JSON-RPC 2.0 错误
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ---------------------------------------------------------------------------
// LSP 基础类型
// ---------------------------------------------------------------------------

// Position LSP 位置
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// Range LSP 范围
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// SymbolKind LSP 符号类型
type SymbolKind int

// 符号类型
const (
	SymbolFile          SymbolKind = 1
	SymbolModule        SymbolKind = 2
	SymbolNamespace     SymbolKind = 3
	SymbolPackage       SymbolKind = 4
	SymbolClass         SymbolKind = 5
	SymbolMethod        SymbolKind = 6
	SymbolProperty      SymbolKind = 7
	SymbolField         SymbolKind = 8
	SymbolConstructor   SymbolKind = 9
	SymbolEnum          SymbolKind = 10
	SymbolInterface     SymbolKind = 11
	SymbolFunction      SymbolKind = 12
	SymbolVariable      SymbolKind = 13
	SymbolConstant      SymbolKind = 14
	SymbolString        SymbolKind = 15
	SymbolNumber        SymbolKind = 16
	SymbolBoolean       SymbolKind = 17
	SymbolArray         SymbolKind = 18
	SymbolObject        SymbolKind = 19
	SymbolKey           SymbolKind = 20
	SymbolNull          SymbolKind = 21
	SymbolEnumMember    SymbolKind = 22
	SymbolStruct        SymbolKind = 23
	SymbolEvent         SymbolKind = 24
	SymbolOperator      SymbolKind = 25
	SymbolTypeParameter SymbolKind = 26
)

// SymbolResult 符号提取结果
type SymbolResult struct {
	Name       string     `json:"name"`
	Kind       SymbolKind `json:"kind"`
	KindName   string     `json:"kindName"`
	Detail     string     `json:"detail,omitempty"`
	DocComment string     `json:"docComment,omitempty"`
	Signature  string     `json:"signature"`
	Code       string     `json:"code"`
	Line       uint32     `json:"line"`
}

// SymbolKindNames SymbolKind 到中文名称的映射
var SymbolKindNames = map[SymbolKind]string{
	SymbolFile:          "file",
	SymbolModule:        "module",
	SymbolNamespace:     "namespace",
	SymbolPackage:       "package",
	SymbolClass:         "class",
	SymbolMethod:        "method",
	SymbolProperty:      "property",
	SymbolField:         "field",
	SymbolConstructor:   "constructor",
	SymbolEnum:          "enum",
	SymbolInterface:     "interface",
	SymbolFunction:      "function",
	SymbolVariable:      "variable",
	SymbolConstant:      "constant",
	SymbolStruct:        "struct",
	SymbolEnumMember:    "enum member",
	SymbolEvent:         "event",
	SymbolOperator:      "operator",
	SymbolTypeParameter: "type parameter",
}

// ---------------------------------------------------------------------------
// LSP 请求/响应中的结构化类型
// ---------------------------------------------------------------------------

// DocumentSymbol LSP 文档符号
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// InitializeParams LSP initialize 请求参数
type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      string             `json:"rootUri,omitempty"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities LSP 客户端能力
type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument"`
}

// TextDocumentClientCapabilities LSP 文本文档客户端能力
type TextDocumentClientCapabilities struct {
	Hover          *HoverCapability          `json:"hover,omitempty"`
	DocumentSymbol *DocumentSymbolCapability `json:"documentSymbol,omitempty"`
}

// HoverCapability hover 能力
type HoverCapability struct {
	ContentFormat []string `json:"contentFormat,omitempty"`
}

// DocumentSymbolCapability documentSymbol 能力
type DocumentSymbolCapability struct {
	HierarchicalDocumentSymbolSupport bool `json:"hierarchicalDocumentSymbolSupport,omitempty"`
}

// InitializeResult LSP initialize 响应结果
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

// ServerCapabilities LSP 服务器能力
type ServerCapabilities struct {
	TextDocumentSync       any `json:"textDocumentSync,omitempty"`
	HoverProvider          any `json:"hoverProvider,omitempty"`
	DocumentSymbolProvider any `json:"documentSymbolProvider,omitempty"`
}

// ServerInfo LSP 服务器信息
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ---------------------------------------------------------------------------
// 格式化 (textDocument/formatting) 相关类型
// ---------------------------------------------------------------------------

// DocumentFormattingParams textDocument/formatting 请求参数
type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

// FormattingOptions 格式化选项
type FormattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}

// TextEdit LSP 文本编辑
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// ---------------------------------------------------------------------------
// 诊断 (textDocument/publishDiagnostics) 相关类型
// ---------------------------------------------------------------------------

// DiagnosticSeverity 诊断严重程度
type DiagnosticSeverity int

const (
	DiagnosticSeverityError       DiagnosticSeverity = 1
	DiagnosticSeverityWarning     DiagnosticSeverity = 2
	DiagnosticSeverityInformation DiagnosticSeverity = 3
	DiagnosticSeverityHint        DiagnosticSeverity = 4
)

// Diagnostic LSP 诊断信息
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Source   string             `json:"source,omitempty"`
	Code     any                `json:"code,omitempty"` // string | int, LSP 诊断错误码
	Message  string             `json:"message"`
}

// PublishDiagnosticsParams textDocument/publishDiagnostics 通知参数
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ---------------------------------------------------------------------------
// didChange 相关类型
// ---------------------------------------------------------------------------

// DidChangeTextDocumentParams textDocument/didChange 通知参数
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// VersionedTextDocumentIdentifier 带版本号的文档标识
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// TextDocumentContentChangeEvent 文档内容变更事件
type TextDocumentContentChangeEvent struct {
	Range       *Range `json:"range,omitempty"`
	RangeLength int    `json:"rangeLength,omitempty"`
	Text        string `json:"text"`
}

// TextDocumentItem LSP 文本文档项
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// DidOpenTextDocumentParams didOpen 通知参数
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidCloseTextDocumentParams didClose 通知参数
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbolParams documentSymbol 请求参数
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// TextDocumentIdentifier LSP 文本文档标识符
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// HoverParams hover 请求参数
type HoverParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// MarkupContent LSP 标记内容
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Hover LSP hover 结果
type Hover struct {
	Contents any `json:"contents"`
	Range    any `json:"range,omitempty"`
}
