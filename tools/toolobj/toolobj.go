package toolobj

// ToolsList 工具列表
var ToolsList map[string]*Tools = make(map[string]*Tools)

// Scopes 工具命名空间
var Scopes map[string]string = make(map[string]string)

// 启用的命名空间
var EnableScopes map[string]bool = make(map[string]bool)
